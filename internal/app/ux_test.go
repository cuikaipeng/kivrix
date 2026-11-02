package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/egoist/mygo/ui"
)

func TestE2EUnsavedSelection(t *testing.T) {
	for _, action := range []string{"Cancel", "Discard", "Save and continue"} {
		t.Run(action, func(t *testing.T) {
			mr := miniredis.RunT(t)
			mr.Set("first", "original")
			mr.Set("second", "other value")
			h := newUIFlow(t)
			s := h.connect(mr.Addr(), "")
			h.selectKey("first")
			h.fill("Code editor", "draft")
			h.click("second")
			if h.a.editGuard == nil || s.valKey != "first" || !s.editorModified() {
				t.Fatal("selection discarded the draft before confirmation")
			}
			h.click(action)
			if action == "Cancel" {
				if h.a.editGuard != nil || s.valKey != "first" || !s.sel.Has("first") || s.strEditor.Buffer.String() != "draft" {
					t.Fatal("cancel did not preserve the selection and draft")
				}
			} else {
				h.wait("confirmed selection", func() bool { return s.valKey == "second" && !s.valLoading })
			}
			want := "original"
			if action == "Save and continue" {
				want = "draft"
			}
			if got, _ := mr.Get("first"); got != want {
				t.Fatalf("saved %q, want %q", got, want)
			}
		})
	}
}

func TestE2EUnsavedFailedSaveAndNavigationCancel(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.Set("first", "original")
	mr.Set("second", "other")
	h := newUIFlow(t)
	s := h.connect(mr.Addr(), "")
	h.selectKey("first")
	h.fill("Code editor", "draft")
	h.click("second")
	mr.SetError("ERR save denied")
	h.click("Save and continue")
	h.wait("failed guarded save", func() bool { return !s.valueSaving })
	if h.a.editGuard == nil || !h.ui.HasText("save denied") || s.valKey != "first" || !s.editorModified() {
		t.Fatal("failed save navigated away or hid the draft")
	}
	mr.SetError("")
	h.click("Cancel")
	for _, action := range []func(){s.reloadValue, s.startScan, func() { s.switchDB(1) }, func() { h.a.closeSession(0) }} {
		action()
		h.ui.Frame()
		if h.a.editGuard == nil {
			t.Fatal("destructive navigation was unguarded")
		}
		h.click("Cancel")
		if s.closed || s.db != 0 || s.dbChoice != "db0" || s.valKey != "first" || s.strEditor.Buffer.String() != "draft" {
			t.Fatal("cancel changed the connection, database or draft")
		}
	}
	s.search, s.keyType = "second", "hash"
	s.startScan()
	h.ui.Frame()
	h.click("Cancel")
	if s.search != "" || s.keyType != "All types" {
		t.Fatal("cancel retained filters that were never applied")
	}
	h.click("CLI")
	h.fill("Command input", "SELECT 1")
	h.click("Run")
	h.click("Cancel")
	if s.cliBusy || s.cliInput != "SELECT 1" || s.db != 0 {
		t.Fatal("cancel left SELECT busy or changed the database")
	}
}

func TestE2EUnsavedDisconnectAll(t *testing.T) {
	h := newUIFlow(t)
	for i := 0; i < 2; i++ {
		st, mr := testStore(t)
		mr.Set("value", "original")
		s := h.a.openSession(Profile{ID: fmt.Sprint(i)}, *st, "")
		s.keys.add("value")
		h.ui.Frame()
		h.selectKey("value")
		h.fill("Code editor", "draft")
	}
	h.a.disconnectAll()
	h.ui.Frame()
	h.click("Discard")
	if len(h.a.sessions) != 2 || h.a.editGuard == nil {
		t.Fatal("closed connections before every draft was resolved")
	}
	h.click("Cancel")
	if len(h.a.sessions) != 2 || !h.a.sessions[1].editorModified() {
		t.Fatal("cancel lost the remaining draft")
	}
	h.a.disconnectAll()
	h.ui.Frame()
	h.click("Save and continue")
	h.wait("disconnect all", func() bool { return len(h.a.sessions) == 0 })
}

func TestReadOnlyStoreRejectsEveryWritePath(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.Set("safe", "original")
	var st Store
	if err := st.Connect(ConnConfig{Addr: mr.Addr(), ConnectionOptions: ConnectionOptions{ReadOnly: true}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	ctx := context.Background()
	for _, cmd := range [][]string{
		{"SET", "safe", "changed"}, {"DEL", "safe"}, {"FLUSHDB"}, {"FLUSHALL"},
		{"EVAL", "return redis.call('DEL',KEYS[1])", "1", "safe"}, {"FCALL", "f", "1", "safe"},
		{"CONFIG", "SET", "maxmemory", "1"}, {"SLOWLOG", "RESET"}, {"PUBLISH", "news", "message"},
		{"XGROUP", "CREATE", "stream", "g", "$", "MKSTREAM"}, {"XREADGROUP", "GROUP", "g", "c", "STREAMS", "stream", ">"},
		{"XACK", "stream", "g", "0-1"}, {"JSON.SET", "json", "$", "{}"}, {"GETDEL", "safe"},
	} {
		if _, err := st.Do(ctx, cmd...); !errors.Is(err, errReadOnly) {
			t.Fatalf("%v: %v", cmd, err)
		}
	}
	pipe := st.client.Pipeline()
	pipe.Get(ctx, "safe")
	pipe.Set(ctx, "safe", "bad pipeline", 0)
	if _, err := pipe.Exec(ctx); !errors.Is(err, errReadOnly) {
		t.Fatalf("pipeline: %v", err)
	}
	if got, err := st.GetValue(ctx, "safe"); err != nil || got.Str != "original" {
		t.Fatalf("read: %+v %v", got, err)
	}
	if err := st.SelectDB(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetString(ctx, "safe", "new DB"); !errors.Is(err, errReadOnly) {
		t.Fatalf("mode lost on SELECT: %v", err)
	}
	path := filepath.Join(t.TempDir(), "data.csv")
	if err := os.WriteFile(path, []byte("key,type,value\nsafe,string,overwrite\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := importSessionCSVWithOptions(ctx, &st, path, defaultCSVImportOptions()); !errors.Is(err, errReadOnly) {
		t.Fatalf("import: %v", err)
	}
	if got, _ := mr.Get("safe"); got != "original" {
		t.Fatal("read-only connection mutated data")
	}
}

func TestE2EReadOnlyConnectionAndPreview(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(readOnly), func(t *testing.T) {
			mr := miniredis.RunT(t)
			mr.Set("large", strings.Repeat("value", maxEditorBytes/2))
			mr.Set("small", "original")
			h := newUIFlow(t)
			h.a.form.advanced.ReadOnly, h.a.form.advanced.Environment = readOnly, "Production"
			s := h.connect(mr.Addr(), "")
			h.selectKey("large")
			if !s.val.PreviewOnly || len(s.val.Str) != 64<<10 || !s.strEditor.ReadOnly || s.canSaveValue() || !h.ui.HasText("Copy preview") {
				t.Fatal("large value did not use a bounded read-only preview")
			}
			before := s.strEditor.Buffer.String()
			h.fill("Code editor", "must not edit")
			if s.strEditor.Buffer.String() != before {
				t.Fatal("preview was editable")
			}
			var saveErr error
			s.saveString(func(err error) { saveErr = err })
			if !errors.Is(saveErr, errReadOnly) {
				t.Fatal("preview could be saved")
			}
			h.selectKey("small")
			if readOnly {
				if !s.strEditor.ReadOnly || s.canSaveValue() || !h.ui.HasText("Read-only") {
					t.Fatal("read-only editor is writable")
				}
				h.a.openNewKey(s)
				if h.a.newKeyOpen {
					t.Fatal("read-only write UI opened")
				}
				h.click("CLI")
				if e := h.command("SET small changed"); !e.err {
					t.Fatal("CLI bypassed read-only mode")
				}
			}
			ps, err := loadProfiles(h.a.profilesPath)
			if err != nil || len(ps) != 1 || ps[0].ReadOnly != readOnly || ps[0].Environment != "Production" {
				t.Fatal("connection safety settings were not persisted")
			}
		})
	}
}

func TestE2ETypedDeleteConfirmation(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.Set("orders/one", "v")
	mr.Set("orders:keep", "v")
	h := newUIFlow(t)
	h.a.form.advanced.Environment, h.a.form.advanced.Separator = "Production", "/"
	s := h.connect(mr.Addr(), "")
	h.a.askDeleteNamespace(s, "orders")
	h.ui.Frame()
	if !h.ui.HasText("Connection: Flow test") || !h.ui.HasText("Database: db0") || !h.ui.HasText("orders/") {
		t.Fatal("confirmation hid its target or used the wrong separator")
	}
	h.click("Delete")
	if !h.a.confirmOpen || !mr.Exists("orders/one") {
		t.Fatal("deletion did not require confirmation text")
	}
	h.fill("Confirmation text", "DELETE")
	h.click("Delete")
	h.wait("namespace deletion", func() bool { return !s.ioBusy && !mr.Exists("orders/one") })
	if !mr.Exists("orders:keep") {
		t.Fatal("deleted a different namespace")
	}
	h.a.askFlush(s)
	h.ui.Frame()
	h.fill("Confirmation text", "db1")
	h.click("Flush")
	if !h.a.confirmOpen {
		t.Fatal("wrong database confirmation was accepted")
	}
	h.click("Cancel")
}

func TestCSVProgressCancellationKeepsPartialCounts(t *testing.T) {
	st, mr := testStore(t)
	keys := []string{"one", "two", "three", "four"}
	for _, key := range keys {
		mr.Set(key, "value")
	}
	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(t.TempDir(), "partial.csv")
	n, skipped, err := exportSessionCSVWithTTL(ctx, st, path, keys, true, func(done, skipped int) {
		if done >= 2 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || n != 2 || skipped != 0 {
		t.Fatalf("export %d %d %v", n, skipped, err)
	}
	for _, key := range keys {
		mr.Del(key)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	n, skipped, err = importSessionCSVWithOptions(ctx, st, path, defaultCSVImportOptions(), func(done, skipped int) {
		if done >= 1 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || n != 1 || skipped != 0 || !mr.Exists("one") || mr.Exists("two") {
		t.Fatalf("import %d %d %v", n, skipped, err)
	}
}

func TestE2ETransferCancelAndCleanup(t *testing.T) {
	h := newUIFlow(t)
	s := testSession(h.a, ConnConfig{Addr: "localhost:6379"})
	ctx, cancel := context.WithCancel(context.Background())
	s.ioBusy, s.ioCancel = true, cancel
	report := s.beginTransfer("Exporting", 100)
	for i := 0; i <= 50; i++ {
		report(i, 2)
	}
	h.wait("coalesced progress", func() bool { return s.ioDone == 50 })
	if !h.ui.HasText("52 / 100 processed") {
		t.Fatal("transfer progress not visible")
	}
	h.click("Cancel transfer")
	if ctx.Err() == nil {
		t.Fatal("cancel did not reach the worker")
	}
	s.close()
	if s.ioProgress != nil || s.ioTimer != nil || s.ioCancel != nil {
		t.Fatal("close retained transfer callbacks")
	}
}

func TestE2EPaletteConnectionsAndRecentKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.Set("first", "one")
	mr.Select(1)
	mr.Set("recent-db1", "two")
	h := newUIFlow(t)
	s := h.connect(mr.Addr(), "")
	s.switchDB(1)
	h.wait("db1", func() bool { return s.db == 1 && !s.scanning })
	h.selectKey("recent-db1")
	s.switchDB(0)
	h.wait("db0", func() bool { return s.db == 0 && !s.scanning })
	h.ui.Key(ui.Cmd, ui.KeyK)
	h.ui.Frame()
	h.fill("Search commands, connections or recent keys", "recent-db1")
	h.ui.Key(0, ui.KeyEnter)
	h.ui.Frame()
	h.wait("recent key in its own database", func() bool { return s.db == 1 && s.valKey == "recent-db1" && !s.valLoading })
	h.a.closeSession(0)
	h.ui.Frame()
	h.ui.Key(ui.Cmd, ui.KeyK)
	h.ui.Frame()
	h.fill("Search commands, connections or recent keys", "Open connection:")
	h.ui.Key(0, ui.KeyEnter)
	h.ui.Frame()
	h.wait("reopened connection", func() bool { return h.a.cur() != nil && !h.a.connConnecting })
	h.ui.Key(ui.Cmd, ui.KeyK)
	h.ui.Frame()
	h.fill("Search commands, connections or recent keys", "Use ")
	h.ui.Key(0, ui.KeyDown)
	h.ui.Frame()
	if h.a.paletteIndex != 1 {
		t.Fatal("down arrow did not move palette selection")
	}
	h.ui.Key(0, ui.KeyEnter)
	h.ui.Frame()
	if h.a.prefs.Theme != "Light" || h.a.paletteOpen {
		t.Fatal("Enter did not run the highlighted command")
	}
}

func TestE2ESidebarAndCompactConnectionForm(t *testing.T) {
	h := newUIFlow(t)
	h.ui.SetSize(760, 520)
	h.ui.Frame()
	for _, label := range []string{"Connect", "Test connection"} {
		r, ok := h.ui.Find(label)
		if !ok || r.Y < 0 || r.Y+r.H > 520 {
			t.Fatalf("%s not visible: %+v", label, r)
		}
	}
	h.a.form.showAdvanced = true
	h.ui.Frame()
	if r, ok := h.ui.Find("Connect"); !ok || r.Y+r.H > 520 {
		t.Fatal("advanced fields pushed the action out of view")
	}
	for i := 0; i < 80 && !h.ui.Focused("Resize sidebar"); i++ {
		h.ui.Key(0, ui.KeyTab)
	}
	if !h.ui.Focused("Resize sidebar") {
		t.Fatal("sidebar resize handle is not reachable by keyboard")
	}
	h.ui.Key(0, ui.KeyRight)
	h.ui.Frame()
	if h.a.prefs.SidebarWidth <= sidebarW {
		t.Fatal("sidebar edge did not resize")
	}
	want := h.a.prefs.SidebarWidth
	h.ui.Key(ui.Cmd, ui.KeyB)
	h.ui.Frame()
	if !h.a.prefs.SidebarHidden {
		t.Fatal("sidebar shortcut did not collapse")
	}
	ps, err := loadPreferences(h.a.prefsPath)
	if err != nil || !ps.SidebarHidden || ps.SidebarWidth != want {
		t.Fatal("sidebar state was not persisted")
	}
	h.ui.Key(ui.Cmd, ui.KeyB)
	h.ui.Frame()
	if h.a.prefs.SidebarHidden || h.a.prefs.SidebarWidth != want {
		t.Fatal("sidebar did not restore its width")
	}
	// Teardown also prevents a delayed preference write escaping the fixture.
	if h.a.prefsSaveTimer != nil {
		h.a.prefsSaveTimer.Stop()
	}
}

func TestE2ESidebarMotion(t *testing.T) {
	h := newUIFlow(t)
	h.ui.SetSize(760, 520)
	h.a.prefs.SidebarWidth = 280.5
	h.ui.Frame()
	h.fill("Name", "draft")
	fieldX := func() float32 {
		r, ok := h.ui.Find("Name")
		if !ok {
			t.Fatal("connection form disappeared during sidebar animation")
		}
		return r.X
	}
	openX := fieldX()
	h.ui.Key(ui.Cmd, ui.KeyB)
	h.ui.Advance(80 * time.Millisecond)
	h.ui.Frame()
	midX := fieldX()
	if midX >= openX || midX <= openX-281 {
		t.Fatalf("sidebar did not slide: open=%v, mid=%v", openX, midX)
	}
	// Reverse before the first animation finishes, without jumping or losing focus.
	h.ui.Key(ui.Cmd, ui.KeyB)
	if got := fieldX(); got != midX {
		t.Fatalf("reversing the animation jumped from %v to %v", midX, got)
	}
	h.ui.Advance(300 * time.Millisecond)
	h.ui.Frame()
	h.ui.Type("x")
	if fieldX() != openX || h.a.form.name != "draftx" {
		t.Fatal("sidebar animation did not restore the layout and input focus")
	}
	h.click("Hide sidebar")
	h.ui.Advance(300 * time.Millisecond)
	h.ui.Frame()
	if delta := openX - fieldX(); delta < 280 || delta > 281 {
		t.Fatalf("collapsed sidebar still occupies space: moved %v", delta)
	}
	if h.ui.HasText("Connection actions") || h.ui.HasText("Resize sidebar") {
		t.Fatal("hidden sidebar still exposes its controls")
	}
	if h.ui.FramePending() {
		t.Fatal("collapsed sidebar kept requesting animation frames")
	}
	h.click("Show sidebar")
	h.ui.Advance(300 * time.Millisecond)
	h.ui.Frame()
	if fieldX() != openX || !h.ui.HasText("Resize sidebar") || h.a.prefs.SidebarWidth != 280.5 {
		t.Fatal("sidebar did not restore its saved width and resize handle")
	}
	if h.ui.FramePending() {
		t.Fatal("expanded sidebar kept requesting animation frames")
	}
}

func TestSidebarReducedMotion(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		t.Run(fmt.Sprint(hidden), func(t *testing.T) {
			a := newApp()
			a.prefs.SidebarHidden = hidden
			tt := ui.NewTester(a.view, 760, 520)
			if tt.FramePending() {
				t.Fatal("sidebar animated on first render")
			}
			tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
			for i := 0; i < 2; i++ {
				tt.Key(ui.Cmd, ui.KeyB)
				tt.Frame()
				if tt.HasText("Connection actions") == a.prefs.SidebarHidden || tt.FramePending() {
					t.Fatal("reduced motion did not immediately show the final sidebar state")
				}
			}
		})
	}
}

func TestE2ESidebarEdgeResize(t *testing.T) {
	h := newUIFlow(t)
	h.fill("Name", "draft")
	r, ok := h.ui.Find("Resize sidebar")
	if !ok {
		t.Fatal("missing sidebar resize target")
	}
	x, y := r.X+r.W/2, r.Y+40
	h.ui.Move(x, y)
	if h.ui.Cursor() != ui.CursorResizeColumn {
		t.Fatal("card edge did not show the resize cursor")
	}
	h.ui.Press(x, y)
	h.ui.Move(x+40, y)
	h.ui.Release(x+40, y)
	if h.a.prefs.SidebarWidth != sidebarW+40 {
		t.Fatal("card edge did not follow the drag")
	}
	h.ui.Type("x")
	if h.a.form.name != "draftx" {
		t.Fatal("resizing took keyboard focus from the input")
	}
	r, _ = h.ui.Find("Resize sidebar")
	x = r.X + r.W/2
	h.ui.Press(x, y)
	h.ui.Move(x-1000, y)
	h.ui.Move(x-950, y)
	if h.a.prefs.SidebarWidth != 240 {
		t.Fatal("dragging past the minimum caused the sidebar to drift")
	}
	h.ui.Release(x-950, y)
	h.click("Resize sidebar")
	if err := h.ui.Click("Resize sidebar"); err != nil {
		t.Fatal(err)
	}
	if h.a.prefs.SidebarWidth != sidebarW {
		t.Fatal("double click did not restore the default width")
	}
	h.ui.Move(700, 400)
	if h.ui.Cursor() == ui.CursorResizeColumn {
		t.Fatal("resize cursor remained outside the card edge")
	}
}

func TestRecentKeysStayBounded(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{})
	for i := 0; i < 30; i++ {
		s.valKey = fmt.Sprint(i)
		a.rememberKey(s)
	}
	a.rememberKey(s)
	s.valKey = strings.Repeat("x", 4097)
	a.rememberKey(s)
	if len(a.recentKeys) != 20 || a.recentKeys[0].key != "29" {
		t.Fatal("recent history is unbounded or duplicated")
	}
}

func TestE2EUpdatedLayouts(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.Set("invoice-1001", "{\n  \"status\": \"pending\",\n  \"total\": 199\n}")
	mr.Set("invoice-1002", "paid")
	h := newUIFlow(t)
	capture := func(name, locale, theme string, width, height int) {
		h.a.prefs.Language, h.a.prefs.Theme = locale, theme
		h.ui.SetLocale(locale)
		h.ui.SetSize(width, height)
		h.ui.Frame()
		saveParityView(t, h.ui, name)
		h.a.prefs.Language, h.a.prefs.Theme = "en", "Light"
		h.ui.SetLocale("en")
		h.ui.SetSize(1180, 760)
		h.ui.Frame()
	}
	capture("ux-01-connection-compact", "zh-Hans", "Light", 760, 520)
	h.a.form.showAdvanced = true
	capture("ux-02-connection-advanced", "en", "Dark", 760, 520)
	h.a.form.advanced.Environment = "Production"
	s := h.connect(mr.Addr(), "")
	h.selectKey("invoice-1001")
	h.fill("Code editor", "{\n  \"status\": \"paid\",\n  \"total\": 199\n}")
	capture("ux-03-dirty-editor", "zh-Hans", "Light", 1180, 760)
	capture("ux-04-editor-compact", "en", "Dark", 760, 520)
	h.click("invoice-1002")
	capture("ux-05-unsaved-guard", "zh-Hans", "Light", 760, 520)
	h.click("Cancel")
	h.a.openPalette()
	h.a.paletteQuery = "invoice"
	capture("ux-06-palette", "en", "Light", 760, 520)
	h.a.paletteOpen = false
	s.markEditorsSaved()
	s.cfg.ReadOnly = true
	capture("ux-07-read-only", "zh-Hans", "Light", 760, 520)
}

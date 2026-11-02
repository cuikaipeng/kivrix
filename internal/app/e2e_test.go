package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	"github.com/egoist/mygo/ui"
)

// Drive the actual view, workers and Redis client. Only the native event loop
// is replaced: every completion is queued and applied by the test's UI thread.
// These tests deliberately do not use t.Parallel (the secret fixture is shared).
type uiFlow struct {
	t    *testing.T
	a    *App
	ui   *ui.Tester
	jobs chan func()
	done chan struct{}
}

func newUIFlow(t *testing.T) *uiFlow {
	t.Helper()
	h := &uiFlow{t: t, a: newApp(), jobs: make(chan func(), 128), done: make(chan struct{})}
	h.a.prefs.Language = "en"
	h.a.profilesPath = filepath.Join(t.TempDir(), "profiles.json")
	h.a.prefsPath = filepath.Join(filepath.Dir(h.a.profilesPath), "preferences.json")
	h.a.dispatch = func(fn func()) {
		select {
		case h.jobs <- fn:
		case <-h.done:
		}
	}
	h.ui = ui.NewTester(h.a.view, 1180, 760)
	h.ui.SetLocale("en")
	t.Cleanup(func() {
		if t.Failed() {
			h.snapshot()
		}
		h.a.closed = true
		if h.a.updateCancel != nil {
			h.a.updateCancel()
		}
		if h.a.prefsSaveTimer != nil {
			h.a.prefsSaveTimer.Stop()
		}
		for _, s := range h.a.sessions {
			s.close()
		}
		// A connection completing during teardown must still be closed.
		if h.a.connConnecting || h.a.restoring {
			h.wait("connection cleanup", func() bool { return !h.a.connConnecting && !h.a.restoring })
		}
		close(h.done)
	})
	return h
}

func (h *uiFlow) snapshot() {
	if dir := os.Getenv("KIVRIX_TEST_ARTIFACTS"); dir != "" {
		name := strings.NewReplacer("/", "_", " ", "_").Replace(h.t.Name()) + ".png"
		if os.MkdirAll(dir, 0o755) == nil {
			if f, err := os.Create(filepath.Join(dir, name)); err == nil {
				defer f.Close()
				if err := png.Encode(f, h.ui.Image()); err == nil {
					h.t.Logf("UI failure screenshot: %s", f.Name())
				}
			}
		}
	}
	h.t.Logf("visible UI: %q", h.ui.Texts())
}

func (h *uiFlow) wait(label string, ready func() bool) {
	h.t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for !ready() {
		select {
		case fn := <-h.jobs:
			fn()
			h.ui.Frame()
		case <-poll.C: // predicates may observe the Redis server, not a UI callback
		case <-deadline.C:
			h.t.Fatalf("timed out waiting for %s (connection error: %s)", label, h.a.connErr.text(h.a))
		}
	}
	h.ui.Frame()
}

// Hold a completed worker result to reproduce an unlucky delivery order without
// sleeps. Call only with an isolated session and command logging disabled.
func (h *uiFlow) nextUpdate() func() {
	h.t.Helper()
	select {
	case fn := <-h.jobs:
		return fn
	case <-time.After(5 * time.Second):
		h.t.Fatal("worker did not publish a completion")
		return nil
	}
}

func (h *uiFlow) click(label string) {
	h.t.Helper()
	h.ui.Advance(600 * time.Millisecond) // independent clicks, not an accidental double click
	if err := h.ui.Click(label); err != nil {
		h.t.Fatal(err)
	}
	h.ui.Frame()
}

func (h *uiFlow) fill(label, value string) {
	h.t.Helper()
	h.click(label)
	h.ui.Key(ui.Cmd, ui.KeyA)
	h.ui.Type(value)
	h.ui.Frame()
}

func (h *uiFlow) menu(label string) {
	h.t.Helper()
	if err := h.ui.ChooseMenuItem(label); err != nil {
		h.t.Fatal(err)
	}
	h.ui.Frame()
}

func (h *uiFlow) connect(addr, password string) *session {
	h.t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		h.t.Fatal(err)
	}
	h.fill("Name", "Flow test")
	h.fill("Host", host)
	h.fill("Port", port)
	if password != "" {
		h.fill("Password", password)
	}
	h.click("Connect")
	h.wait("connection and initial scan", func() bool {
		s := h.a.cur()
		return !h.a.connConnecting && s != nil && !s.scanning
	})
	return h.a.cur()
}

func (h *uiFlow) selectKey(key string) *session {
	h.t.Helper()
	h.click(key)
	h.wait("value "+key, func() bool {
		s := h.a.cur()
		return s != nil && s.valKey == key && !s.valLoading && s.valErr == ""
	})
	return h.a.cur()
}

func (h *uiFlow) command(command string) cliEntry {
	h.t.Helper()
	s := h.a.cur()
	n := len(s.cliEntries)
	h.fill("Command input", command)
	h.click("Run")
	h.wait(command, func() bool { return !s.cliBusy && len(s.cliEntries) > n })
	return s.cliEntries[len(s.cliEntries)-1]
}

func TestE2EConnectionAndKeyLifecycle(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireAuth("e2e-password")
	h := newUIFlow(t)
	s := h.connect(mr.Addr(), "e2e-password")
	data, err := os.ReadFile(h.a.profilesPath)
	if err != nil || strings.Contains(string(data), "e2e-password") {
		t.Fatalf("unsafe profile persistence: %v", err)
	}
	if fakeSecrets["profile/"+s.id+"/redis"] != "e2e-password" {
		t.Fatal("password was not stored separately")
	}

	h.click("New key")
	h.fill("Key", "flow-key")
	h.fill("The key's value", "original")
	h.click("Create")
	h.wait("new key", func() bool { return !h.a.newKeyOpen && s.valKey == "flow-key" && !s.valLoading })
	if got, _ := mr.Get("flow-key"); got != "original" {
		t.Fatalf("created %q", got)
	}
	mr.SetTTL("flow-key", time.Hour)
	h.fill("Code editor", "edited through UI")
	h.click("Save")
	h.wait("saved editor", func() bool { return !s.valueSaving && !s.strEditor.Buffer.Modified() })
	if got, _ := mr.Get("flow-key"); got != "edited through UI" || mr.TTL("flow-key") <= 0 {
		t.Fatalf("save lost value/TTL: %q", got)
	}

	if err := h.ui.RightClick("flow-key"); err != nil {
		t.Fatal(err)
	}
	h.menu("Rename…")
	h.fill("New name", "renamed-key")
	h.click("Rename")
	h.wait("renamed key", func() bool { return !h.a.renameOpen && s.valKey == "renamed-key" })
	if mr.Exists("flow-key") || !mr.Exists("renamed-key") {
		t.Fatal("rename not reflected in Redis")
	}
	if err := h.ui.RightClick("renamed-key"); err != nil {
		t.Fatal(err)
	}
	h.menu("Delete Key…")
	h.click("Cancel")
	if !mr.Exists("renamed-key") {
		t.Fatal("cancel deleted data")
	}
	if err := h.ui.RightClick("renamed-key"); err != nil {
		t.Fatal(err)
	}
	h.menu("Delete Key…")
	h.click("Delete")
	h.wait("deleted key", func() bool { return s.valKey == "" && s.keys.node("renamed-key") == nil })
	if mr.Exists("renamed-key") {
		t.Fatal("delete did not reach Redis")
	}
}

func TestE2EFailedAuthThenRetry(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireAuth("correct")
	h := newUIFlow(t)
	host, port, _ := net.SplitHostPort(mr.Addr())
	h.fill("Host", host)
	h.fill("Port", port)
	h.fill("Password", "wrong")
	h.click("Connect")
	h.wait("authentication error", func() bool { return !h.a.connConnecting })
	if h.a.connErr.key == "" || h.a.cur() != nil || len(h.a.profiles) != 0 {
		t.Fatal("failed authentication adopted a session/profile")
	}
	h.fill("Password", "correct")
	h.click("Connect")
	h.wait("retry", func() bool { return h.a.cur() != nil && !h.a.cur().scanning })
	if h.a.connErr.key != "" {
		t.Fatal("successful retry retained the old error")
	}
}

func TestE2EDatabaseCountsAndSelection(t *testing.T) {
	mr := miniredis.RunT(t)
	// miniredis lacks INFO keyspace; expose its actual per-database contents.
	keyspace := func(c *server.Peer, cmd string, args ...string) bool {
		if cmd != "INFO" || len(args) != 1 || args[0] != "keyspace" {
			return false
		}
		var info strings.Builder
		info.WriteString("# Keyspace\r\n")
		for db := 0; db < 16; db++ {
			if count := len(mr.DB(db).Keys()); count > 0 {
				fmt.Fprintf(&info, "db%d:keys=%d,expires=0,avg_ttl=0\r\n", db, count)
			}
		}
		c.WriteBulk(info.String())
		return true
	}
	mr.Server().SetPreHook(keyspace)
	mr.Set("zero-a", "db0")
	mr.Set("zero-b", "db0")
	mr.DB(1).Set("one-a", "db1")
	h := newUIFlow(t)
	h.a.form.dbAliases = "0=Cache\n1=Sessions (active) · team"
	s := h.connect(mr.Addr(), "")
	h.wait("database statistics", func() bool { return s.dbKeys[0] == 2 && s.dbKeys[1] == 1 })
	h.click("Database")
	for _, label := range []string{"db0 (2) · Cache", "db1 (1) · Sessions (active) · team", "db2 (0)"} {
		if !h.ui.HasText(label) {
			t.Fatalf("missing database option %q", label)
		}
	}
	// Refresh while the popup is open, then choose the updated label.
	mr.DB(1).Set("one-b", "db1")
	s.refreshDatabases()
	h.wait("updated database statistics", func() bool { return s.dbKeys[1] == 2 })
	h.click("db1 (2) · Sessions (active) · team")
	h.wait("switch to db1", func() bool { return s.db == 1 && !s.dbSwitching && !s.scanning })
	if s.dbChoice != "db1 · Sessions (active) · team" || s.keys.node("one-a") == nil || s.keys.node("zero-a") != nil {
		t.Fatal("displayed count or alias changed database selection")
	}
	mr.DB(1).Del("one-a")
	mr.DB(1).Del("one-b")
	h.click("Rescan")
	h.wait("empty database statistics", func() bool { return s.dbKeys != nil && s.dbKeys[1] == 0 && !s.scanning })
	if !h.ui.HasText("db1 (0) · Sessions (active) · team") {
		t.Fatal("empty database was not shown as zero")
	}
	mr.SetError("NOPERM statistics unavailable")
	s.refreshDatabases()
	h.wait("unavailable database statistics", func() bool { return s.dbKeys == nil })
	if !h.ui.HasText("db1 (?) · Sessions (active) · team") {
		t.Fatal("failed statistics refresh showed a stale count")
	}
	mr.Server().SetPreHook(keyspace)
	h.ui.Key(ui.Cmd, ui.KeyR)
	h.wait("database statistics retry", func() bool { return s.dbKeys != nil && !s.scanning })
	if !h.ui.HasText("db1 (0) · Sessions (active) · team") || s.db != 1 {
		t.Fatal("statistics retry changed the database or lost the count")
	}
}

func TestE2ECLIAndDatabaseIsolation(t *testing.T) {
	mr := miniredis.RunT(t)
	h := newUIFlow(t)
	s := h.connect(mr.Addr(), "")
	h.click("CLI")
	for _, cmd := range []string{"SET shared db0", "SELECT 1", "SET shared db1"} {
		if e := h.command(cmd); e.err || e.result != "OK" {
			t.Fatalf("%s: %+v", cmd, e)
		}
	}
	if s.db != 1 || s.dbChoice != "db1" {
		t.Fatal("SELECT did not update the browser database")
	}
	if e := h.command("GET shared"); e.result != "db1" {
		t.Fatalf("wrong DB: %+v", e)
	}
	h.command("SELECT 0")
	if e := h.command("GET shared"); e.result != "db0" {
		t.Fatalf("wrong DB: %+v", e)
	}
	if e := h.command("AUTH do-not-record"); !e.err || strings.Contains(e.command, "do-not-record") {
		t.Fatalf("sensitive command: %+v", e)
	}
	if strings.Contains(strings.Join(s.cliHistory, " "), "do-not-record") {
		t.Fatal("password leaked into history")
	}
	h.fill("Command input", "")
	h.ui.Key(0, ui.KeyUp)
	if s.cliInput != "GET shared" {
		t.Fatalf("history selected %q", s.cliInput)
	}
}

func TestE2EPagingAndCSVTransfer(t *testing.T) {
	mr := miniredis.RunT(t)
	for i := 0; i < 12; i++ {
		mr.Set(fmt.Sprintf("item-%02d", i), "value")
	}
	for i := 0; i < 7; i++ {
		mr.RPush("queue", fmt.Sprint(i))
	}
	h := newUIFlow(t)
	h.a.prefs.ScanSize, h.a.prefs.ValuePageSize = 3, 2
	s := h.connect(mr.Addr(), "")
	// miniredis deliberately ignores SCAN COUNT; real Redis tests cover key paging.
	if s.keys.count != 13 {
		t.Fatalf("loaded %d keys", s.keys.count)
	}
	h.selectKey("queue")
	for s.valMore {
		h.click("Load more rows")
		h.wait("value page", func() bool { return !s.valLoadingMore })
	}
	if len(s.val.Items) != 7 {
		t.Fatalf("loaded %d rows", len(s.val.Items))
	}

	// Start at the file dialog boundary; OS dialogs still need a manual check.
	// Wait for both I/O and UI completion.
	p := filepath.Join(t.TempDir(), "transfer.csv")
	s.exportCSV(p, []string{"queue"}, true)
	h.wait("CSV export", func() bool { return !s.ioBusy })
	if s.statusErr {
		t.Fatal(s.status.text(h.a))
	}
	mr.Del("queue")
	s.importCSV(p, defaultCSVImportOptions())
	h.wait("CSV import and rescan", func() bool { return !s.ioBusy && !s.scanning })
	if s.statusErr {
		t.Fatal(s.status.text(h.a))
	}
	got, err := s.store.GetValue(context.Background(), "queue")
	if err != nil || len(got.Items) != 7 {
		t.Fatalf("CSV restore: %+v %v", got, err)
	}
}

func TestE2EEncodedSaveFailureAndRetry(t *testing.T) {
	mr := miniredis.RunT(t)
	original := base64.StdEncoding.EncodeToString([]byte("original"))
	mr.Set("encoded", original)
	h := newUIFlow(t)
	h.a.prefs.DefaultDecode = "Base64"
	s := h.connect(mr.Addr(), "")
	h.selectKey("encoded")
	h.wait("decoded editor", func() bool { return !s.decodedBusy && s.decodedEditor.Buffer != nil })
	if got := s.decodedEditor.Buffer.String(); got != "original" {
		t.Fatalf("decoded %q", got)
	}
	h.fill("Code editor", "edited")
	mr.SetError("ERR simulated write failure")
	h.click("Save")
	h.wait("failed save", func() bool { return !s.valueSaving })
	if !s.statusErr || !s.decodedEditor.Buffer.Modified() || !h.ui.HasText("simulated write failure") {
		t.Fatal("failed save lost the dirty buffer or hid the error")
	}
	if got, _ := mr.Get("encoded"); got != original {
		t.Fatal("failed save changed Redis")
	}
	mr.SetError("")
	h.click("Save")
	h.wait("retried save", func() bool { return !s.valueSaving && !s.decodedEditor.Buffer.Modified() })
	want := base64.StdEncoding.EncodeToString([]byte("edited"))
	if got, _ := mr.Get("encoded"); got != want || s.strEditor.Buffer.String() != want || s.statusErr {
		t.Fatal("retry did not update Redis and both editor buffers")
	}
}

func TestE2EPubSubLifecycle(t *testing.T) {
	mr := miniredis.RunT(t)
	h := newUIFlow(t)
	s := h.connect(mr.Addr(), "")
	h.click("Pub/Sub")
	h.fill("Enter a channel or pattern.", "flow-news")
	h.click("Subscribe")
	h.wait("subscription", func() bool { return mr.PubSubNumSub("flow-news")["flow-news"] == 1 })
	h.fill("channel", "flow-news")
	h.fill("message", "hello through the UI")
	h.click("Publish")
	h.wait("published message", func() bool { return !s.pubPublishBusy && len(s.pubMessages) == 1 })
	if !h.ui.HasText("hello through the UI") || s.pubPayload != "" || s.pubMessages[0].channel != "flow-news" {
		t.Fatal("publish/receive completion did not reach the UI")
	}
	h.click("Browser")
	h.wait("unsubscribe on leaving tool", func() bool { return mr.PubSubNumSub("flow-news")["flow-news"] == 0 })
	if s.pubRunning || s.pubSub != nil || s.pubCancel != nil {
		t.Fatal("leaving Pub/Sub retained a live subscription")
	}
	h.click("Close Flow test")
	h.wait("connection cleanup", func() bool { return mr.CurrentConnectionCount() == 0 })
	if h.a.cur() != nil || !s.closed {
		t.Fatal("closed connection remained active")
	}
}

func TestAsyncReadCompletionAfterSelectionOrClose(t *testing.T) {
	for _, action := range []string{"select another key", "close session"} {
		t.Run(action, func(t *testing.T) {
			st, mr := testStore(t)
			mr.Set("first", "old result")
			mr.Set("second", "current result")
			h := newUIFlow(t)
			// Skip connection/metadata jobs so the only queued job is this read.
			s := h.a.openSession(Profile{ID: "delayed"}, *st, "")
			s.cmdEvents.close()
			s.keys.add("first")
			s.keys.add("second")
			h.ui.Frame()
			h.click("first")
			oldReply := h.nextUpdate()
			if !s.valLoading {
				t.Fatal("read was not held before UI completion")
			}
			if action == "close session" {
				h.a.closeSession(0)
			} else {
				h.selectKey("second")
			}
			oldReply()
			h.ui.Frame()
			if s.val.Str == "old result" {
				t.Fatal("stale read overwrote the current view")
			}
			if action == "select another key" && (s.valKey != "second" || s.val.Str != "current result" || s.valLoading) {
				t.Fatal("stale read changed the current selection")
			}
		})
	}
}

func TestAsyncSavePreservesNewerEdits(t *testing.T) {
	st, mr := testStore(t)
	mr.Set("value", "original")
	h := newUIFlow(t)
	s := h.a.openSession(Profile{ID: "delayed"}, *st, "")
	s.cmdEvents.close()
	s.keys.add("value")
	h.ui.Frame()
	h.selectKey("value")
	h.fill("Code editor", "first edit")
	h.click("Save")
	saved := h.nextUpdate()
	h.fill("Code editor", "newer edit")
	saved()
	h.ui.Frame()
	if s.valueSaving || !s.strEditor.Buffer.Modified() || s.strEditor.Buffer.String() != "newer edit" {
		t.Fatal("save completion discarded/marked saved an edit made during the save")
	}
	if got, _ := mr.Get("value"); got != "first edit" {
		t.Fatalf("saved %q", got)
	}
}

func TestAsyncRestoreLastSession(t *testing.T) {
	for _, closeEarly := range []bool{false, true} {
		t.Run(fmt.Sprintf("close_before_completion=%t", closeEarly), func(t *testing.T) {
			mr := miniredis.RunT(t)
			mr.Select(2)
			mr.Set("db-two", "restored value")
			h := newUIFlow(t)
			h.a.profiles = []Profile{{ID: "restore", Name: "Restored", Addr: mr.Addr()}}
			h.a.prefs.Startup, h.a.prefs.LastProfile, h.a.prefs.LastDB = "Last session", "restore", 2
			h.a.restoreLastSession()
			h.a.closed = closeEarly
			h.wait("startup restoration", func() bool { return !h.a.restoring })
			if closeEarly {
				h.wait("late connection cleanup", func() bool { return mr.CurrentConnectionCount() == 0 })
				if h.a.cur() != nil {
					t.Fatal("a closed application adopted a late connection")
				}
				return
			}
			s := h.a.cur()
			if s == nil || s.db != 2 || s.id != "restore" {
				t.Fatal("startup did not restore the last profile/database")
			}
			h.wait("restored key scan", func() bool { return !s.scanning })
			h.selectKey("db-two")
			if s.val.Str != "restored value" {
				t.Fatal("restored session could not load data")
			}
		})
	}
}

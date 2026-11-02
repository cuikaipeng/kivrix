package app

import (
	"context"
	"encoding/json"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestParityCodecRoundTrips(t *testing.T) {
	for _, name := range []string{"None", "Base64", "Base64 URL", "Hex", "URL", "GZip", "Deflate", "Zlib", "ZStd", "LZ4", "Brotli", "Msgpack", "PHP"} {
		t.Run(name, func(t *testing.T) {
			original := `{"n":9007199254740993,"text":"柔软","ok":true}`
			codec := valueCodec{format: "JSON", decode: name}
			encoded, err := codec.save(original)
			if err != nil {
				t.Fatal(err)
			}
			text, resolved, err := codec.open(string(encoded))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, "9007199254740993") {
				t.Fatalf("lost integer precision: %s", text)
			}
			edited := strings.ReplaceAll(text, "柔软", "新的值")
			encoded, err = resolved.save(edited)
			if err != nil {
				t.Fatal(err)
			}
			result, err := decodeValue(encoded, name)
			if err != nil || !json.Valid(result) || !strings.Contains(string(result), "新的值") {
				t.Fatalf("round trip: %q %v", result, err)
			}
		})
	}
}

func TestParityCodecInvalidAndLargeNumbers(t *testing.T) {
	unicodeJSON := valueCodec{format: "JSON Unicode"}
	text, resolved, err := unicodeJSON.open(`{"n":9007199254740993,"text":"\u67d4\u8f6f"}`)
	if err != nil || !strings.Contains(text, "9007199254740993") || !strings.Contains(text, "柔软") {
		t.Fatalf("Unicode JSON precision: %q %v", text, err)
	}
	if _, err := resolved.save(text); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Msgpack", "PHP"} {
		for _, invalid := range []string{`1 2`, `{"n":18446744073709551616}`, `{"n":1e1000}`} {
			if _, err := encodeValue([]byte(invalid), name, ""); err == nil {
				t.Fatalf("%s accepted %s", name, invalid)
			}
		}
	}
	raw := `{"n":18446744073709551615}`
	encoded, err := encodeValue([]byte(raw), "Msgpack", "")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeValue(encoded, "Msgpack")
	if err != nil || !strings.Contains(string(decoded), "18446744073709551615") {
		t.Fatalf("uint64 round trip: %s %v", decoded, err)
	}
}

func TestParityPickleEditing(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python 3 is not installed")
	}
	codec := valueCodec{format: "JSON", decode: "Pickle"}
	raw, err := codec.save(`{"n":9007199254740993,"text":"柔软"}`)
	if err != nil {
		t.Fatal(err)
	}
	text, _, err := codec.open(string(raw))
	if err != nil || !strings.Contains(text, "9007199254740993") || !strings.Contains(text, "柔软") {
		t.Fatalf("pickle: %q %v", text, err)
	}
}

func TestParityBinaryEditingAndAutoDetection(t *testing.T) {
	raw := string([]byte{0, 0xff, 0x80, 0x7f})
	for _, format := range []string{"Hex", "Binary", "BitSet"} {
		codec := valueCodec{format: format, decode: "None"}
		text, resolved, err := codec.open(raw)
		if err != nil {
			t.Fatal(err)
		}
		result, err := resolved.save(text)
		if err != nil || string(result) != raw {
			t.Fatalf("%s: %x %v", format, result, err)
		}
	}
	for _, decode := range []string{"GZip", "Zlib", "ZStd", "LZ4", "Base64"} {
		encoded, err := encodeValue([]byte(`{"a":1}`), decode, "")
		if err != nil {
			t.Fatal(err)
		}
		text, codec, err := (valueCodec{format: "Auto", decode: "Auto"}).open(string(encoded))
		if err != nil || codec.decode != decode || codec.format != "JSON" || !json.Valid([]byte(text)) {
			t.Fatalf("%s: %+v %q %v", decode, codec, text, err)
		}
	}
	for _, raw := range []string{"1234", "hello", `{"a":1}`} {
		if got := detectDecode(raw); got != "None" {
			t.Fatalf("false detection %q: %s", raw, got)
		}
	}
	if _, _, err := (valueCodec{format: "JSON"}).open(strings.Repeat("x", maxFormatBytes+1)); err == nil {
		t.Fatal("editable preview must not be truncated")
	}
	if _, err := (valueCodec{format: "Hex"}).save("not hex"); err == nil {
		t.Fatal("invalid hex accepted")
	}
	if _, err := (valueCodec{format: "Binary"}).save("111"); err == nil {
		t.Fatal("partial byte accepted")
	}
}

func TestParityFilteredKeys(t *testing.T) {
	st, mr := testStore(t)
	ctx := context.Background()
	mr.Set("user:*", "literal")
	mr.Set("user:1", "one")
	mr.HSet("user:2", "field", "value")
	for _, tc := range []struct {
		f    keyFilter
		want []string
	}{
		{keyFilter{"user:*", "Exact", "All types"}, []string{"user:*"}},
		{keyFilter{"user:*", "Exact", "hash"}, nil},
		{keyFilter{"user:*", "Pattern", "hash"}, []string{"user:2"}},
		{keyFilter{"ser", "Contains", "string"}, []string{"user:*", "user:1"}},
	} {
		got, _, err := st.scanFiltered(ctx, "", tc.f, 100)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%+v: %v, %v", tc.f, got, err)
		}
	}
}

func TestParityLargeListSearchAndExport(t *testing.T) {
	st, mr := testStore(t)
	ctx := context.Background()
	items := make([]string, 10020)
	for i := range items {
		items[i] = "ordinary"
	}
	items[10005], items[10015] = "target one", "target two"
	mr.RPush("large", items...)
	p, err := st.GetValuePage(ctx, "large", 0, 0, 1, "*target*")
	if err != nil || len(p.Items) != 1 || p.ListIndices[0] != 10005 || !p.HasMore {
		t.Fatalf("first match: %+v %v", p, err)
	}
	p, err = st.GetValuePage(ctx, "large", 0, p.NextOffset, 1, "*target*")
	if err != nil || len(p.Items) != 1 || p.ListIndices[0] != 10015 {
		t.Fatalf("second match: %+v %v", p, err)
	}
	path := filepath.Join(t.TempDir(), "large.csv")
	if n, skipped, err := exportSessionCSVWithTTL(ctx, st, path, []string{"large"}, true); err != nil || n != 1 || skipped != 0 {
		t.Fatalf("export: %d %d %v", n, skipped, err)
	}
	mr.Del("large")
	if n, _, err := importSessionCSVWithOptions(ctx, st, path, defaultCSVImportOptions()); err != nil || n != 1 {
		t.Fatalf("import: %d %v", n, err)
	}
	got, err := st.client.LRange(ctx, "large", 0, -1).Result()
	if err != nil || !reflect.DeepEqual(got, items) {
		t.Fatalf("round trip length: %d %v", len(got), err)
	}
}

func TestParityBulkRowsValidateBeforeWriting(t *testing.T) {
	st, mr := testStore(t)
	ctx := context.Background()
	mr.RPush("list", "tail")
	if err := st.addRows(ctx, rowEdit{key: "list", typ: "list", bulkText: "first\nsecond", head: true}); err != nil {
		t.Fatal(err)
	}
	got, _ := mr.List("list")
	if !reflect.DeepEqual(got, []string{"first", "second", "tail"}) {
		t.Fatalf("prepend: %v", got)
	}
	if err := st.addRows(ctx, rowEdit{key: "hash", typ: "hash", bulkText: "a=one\nb=two", codec: valueCodec{decode: "Base64"}}); err != nil {
		t.Fatal(err)
	}
	if got := mr.HGet("hash", "b"); got != "dHdv" {
		t.Fatalf("encoded batch: %q", got)
	}
	if err := st.addRows(ctx, rowEdit{key: "hash", typ: "hash", bulkText: "c=1\nd=bad", codec: valueCodec{format: "JSON"}}); err == nil {
		t.Fatal("bad batch accepted")
	}
	if mr.HGet("hash", "c") != "" {
		t.Fatal("partially wrote invalid batch")
	}
}

func TestParityCSVRejectsLossyCollectionEncoding(t *testing.T) {
	for _, v := range []Value{
		{Type: "hash", Pairs: [][2]string{{"field", "\xff"}}},
		{Type: "list", Items: []string{"\xff"}},
		{Type: "zset", Pairs: [][2]string{{"\xff", "1"}}},
		{Type: "stream", Stream: []StreamEntry{{ID: "1-0", Fields: [][2]string{{"field", "\xff"}}}}},
	} {
		if _, err := encodeCSVValue(v); err == nil {
			t.Fatalf("%s binary data would be replaced by JSON", v.Type)
		}
	}
}

func TestParityOptionsAndConnectionURL(t *testing.T) {
	var f connForm
	f.reset()
	if err := f.applyURL("rediss://alice:secret@localhost:6380/2"); err != nil {
		t.Fatal(err)
	}
	f.advanced.Separator, f.advanced.KeyView, f.advanced.DBFilter = "/", "List", "Show"
	f.dbList, f.dbAliases, f.connectTimeout, f.commandTimeout = "0,2", "2=Sessions", "7", "19"
	p := f.profile("p")
	if p.Addr != "localhost:6380" || p.DB != 2 || !p.UseTLS || f.password != "secret" || p.ConnectTimeout != 7 || p.CommandTimeout != 19 {
		t.Fatalf("profile: %+v", p)
	}
	if p.showsDB(1) || !p.showsDB(2) || p.dbLabel(2) != "db2 · Sessions" {
		t.Fatalf("DB options: %+v", p.ConnectionOptions)
	}
	data, _ := json.Marshal(p)
	if strings.Contains(string(data), "secret") {
		t.Fatal("password leaked into profile")
	}
	var copy connForm
	copy.fill(p)
	if got := copy.profile("p"); !reflect.DeepEqual(got.ConnectionOptions, p.ConnectionOptions) {
		t.Fatalf("options round trip: %+v", got.ConnectionOptions)
	}
	if err := f.applyURL("unix:///tmp/kivrix.sock?db=3"); err != nil {
		t.Fatal(err)
	}
	if p := f.profile("unix"); p.Network != "unix" || p.Addr != "/tmp/kivrix.sock" || p.DB != 3 {
		t.Fatalf("unix: %+v", p)
	}
	f.commandTimeout = "bad"
	if validateProfile(f.profile("bad")) == "" {
		t.Fatal("invalid timeout accepted")
	}
}

func TestParityEmptyGroupsAndOrder(t *testing.T) {
	a := newApp()
	a.prefsPath = filepath.Join(t.TempDir(), "preferences.json")
	a.openGroupRename("")
	a.groupCreating = true
	a.renameGroup("", "Empty")
	if groups := a.connGroups(); len(groups) != 1 || groups[0].name != "Empty" || len(groups[0].profiles) != 0 {
		t.Fatalf("groups: %+v", groups)
	}
	prefs, err := loadPreferences(a.prefsPath)
	if err != nil || !reflect.DeepEqual(prefs.Groups, []string{"Empty"}) {
		t.Fatalf("saved groups: %v %v", prefs.Groups, err)
	}
	a.profiles = []Profile{{ID: "a", Name: "A", Group: "Work"}, {ID: "b", Name: "B", Group: "Work"}}
	a.selProfile, a.connSel = "a", 0
	a.moveProfile("b", -1)
	if a.profiles[0].ID != "b" || a.connSel != 1 {
		t.Fatalf("profile order: %+v", a.profiles)
	}
	a.moveGroup("Work", -1)
	if a.groupNames()[0] != "Work" {
		t.Fatal("group order not saved")
	}
	a.deleteGroup("Empty")
	if len(a.groupNames()) != 1 {
		t.Fatalf("group deletion: %v", a.groupNames())
	}
}

func TestParityCLIHistoryKeyboard(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.tool = "cli"
	s.rememberCLI("GET first")
	s.rememberCLI("GET second")
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("Command input"); err != nil {
		t.Fatal(err)
	}
	tt.Type("draft")
	tt.Key(0, ui.KeyUp)
	if s.cliInput != "GET second" {
		t.Fatalf("up: %q", s.cliInput)
	}
	tt.Key(0, ui.KeyUp)
	if s.cliInput != "GET first" {
		t.Fatalf("up twice: %q", s.cliInput)
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	if s.cliInput != "draft" {
		t.Fatalf("draft: %q", s.cliInput)
	}
}

func TestParityHashSortPreservesRowIdentity(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{})
	s.val = Value{Type: "hash", Pairs: [][2]string{{"b", "2"}, {"a", "1"}}}
	s.valRowSel, s.valSort = 0, ui.SortOrder{Column: "field"}
	s.sortHashRows()
	if s.val.Pairs[0][0] != "a" || s.valRowSel != 1 || a.rowAt(s, s.valRowSel).field != "b" {
		t.Fatalf("sorted: %v, selected %d", s.val.Pairs, s.valRowSel)
	}
	s.valSort.Descending = true
	s.sortHashRows()
	if s.val.Pairs[0][0] != "b" || s.valRowSel != 0 {
		t.Fatal("descending order changed selected field")
	}
}

func TestParityViews(t *testing.T) {
	a := newApp()
	a.prefs.Language = "简体中文"
	a.form.showAdvanced = true
	a.form.advanced.DBFilter = "Show"
	a.form.dbList, a.form.dbAliases = "0, 2", "0=缓存\n2=会话"
	tt := ui.NewTester(a.view, 1180, 760)
	for range 3 {
		tt.Frame()
	}
	saveParityView(t, tt, "connections")
	tt.Scroll(700, 650, 0, 650)
	tt.Frame()
	saveParityView(t, tt, "advanced")
	a.prefsOpen = true
	prefs := ui.NewTester(a.settingsView, 780, 860)
	saveParityView(t, prefs, "preferences")
	a.prefsOpen = false
	s := testSession(a, ConnConfig{Name: "Local Redis", Addr: "127.0.0.1:6379"})
	s.keys.add("users:1")
	s.keys.add("users:2")
	s.sel.Add("users:1")
	s.valKey, s.val = "users:1", Value{Type: "hash", Pairs: [][2]string{{"name", "Ada"}, {"role", "admin"}}, Size: 2}
	tt.Frame()
	if !tt.HasText("Ada") {
		t.Fatalf("browser missing value: %v", tt.Texts())
	}
	saveParityView(t, tt, "browser")
	a.openRowAdd(s)
	a.row.bulk = true
	a.row.bulkText = "email=ada@example.com\nactive=true"
	tt.Frame()
	if !a.rowOpen || !tt.HasText(a.tr("Bulk add")) {
		t.Fatalf("bulk editor missing: %v", tt.Texts())
	}
	saveParityView(t, tt, "bulk")
	a.rowOpen = false
	s.val = Value{Type: "string", Str: `{"name":"Ada"}`, Size: 14}
	s.valFormat, s.valDecode = "JSON", "Base64"
	s.decodedEditor.Buffer = editor.NewBuffer("{\n  \"name\": \"Ada\"\n}")
	tt.Frame()
	if !tt.HasText(a.tr("Save")) {
		t.Fatalf("decoded editor missing save: %v", tt.Texts())
	}
	saveParityView(t, tt, "editor")
	// Rebuild through disappearing controls to catch stale element/identity bugs.
	s.tool = "cli"
	tt.Frame()
	s.tool = "browser"
	tt.Frame()
	a.active = -1
	tt.Frame()
	a.active = 0
	tt.Frame()
	if s.app.prefs.refreshInterval() != 5*time.Second {
		t.Fatal("default refresh interval")
	}
}

func saveParityView(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("KIVRIX_TEST_ARTIFACTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

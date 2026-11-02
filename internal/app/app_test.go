package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// fakeSecrets keeps tests off the real secret store.
var fakeSecrets = map[string]string{}

func TestMain(m *testing.M) {
	secretGet = func(key string) ([]byte, error) {
		if v, ok := fakeSecrets[key]; ok {
			return []byte(v), nil
		}
		return nil, mygo.ErrSecretNotFound
	}
	secretSet = func(key string, value []byte) error {
		fakeSecrets[key] = string(value)
		return nil
	}
	secretDelete = func(key string) error {
		delete(fakeSecrets, key)
		return nil
	}
	os.Exit(m.Run())
}

func testSession(a *App, cfg ConnConfig) *session {
	s := newSession(a, Profile{ID: "test", Addr: cfg.Addr}, Store{cfg: cfg}, "")
	a.sessions = []*session{s}
	a.active = 0
	return s
}

func TestHomeView(t *testing.T) {
	a := newApp()
	a.form.addr, a.form.host, a.form.port = "", "", ""
	tt := ui.NewTester(a.view, 900, 600)
	for _, want := range []string{"Connections", "New Connection", "Name", "Host", "Port", "Connect", "Save", "Test connection"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Host"); err != nil {
		t.Fatal(err)
	}
	tt.Type("example.com")
	if err := tt.Click("Port"); err != nil {
		t.Fatal(err)
	}
	tt.Type("6380")
	if a.form.host != "example.com" || a.form.port != "6380" {
		t.Fatalf("addr %q", a.form.addr)
	}
}

func TestProfileFormRoundTrip(t *testing.T) {
	var f connForm
	f.name, f.addr, f.user = "prod", "redis.example.com:6379", "admin"
	f.db, f.tls = "3", true
	f.ssh, f.sshAddr, f.sshUser, f.sshKey = true, "bastion:22", "ops", "/keys/id"
	p := f.profile("id1")
	if p.Name != "prod" || p.Addr != "redis.example.com:6379" || p.DB != 3 || !p.UseTLS {
		t.Fatalf("profile %+v", p)
	}
	if !p.SSH.Enabled || p.SSH.Addr != "bastion:22" || p.SSH.KeyPath != "/keys/id" {
		t.Fatalf("ssh %+v", p.SSH)
	}
	// Passwords never enter the profile.
	var g connForm
	g.fill(p)
	if g.password != "" && g.password != loadSecret("profile/id1/redis") {
		t.Fatalf("unexpected password %q", g.password)
	}
	if g.db != "3" || !g.tls || !g.ssh || g.sshAddr != "bastion:22" {
		t.Fatalf("form %+v", g)
	}
}

func TestProfilesPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "profiles.json")
	if ps, err := loadProfiles(path); err != nil || ps != nil {
		t.Fatalf("missing file: ps %v err %v", ps, err)
	}
	ps := []Profile{
		{ID: "2", Name: "b", Addr: "b:6379"},
		{ID: "1", Name: "a", Addr: "a:6379", UseTLS: true},
	}
	if err := saveProfiles(path, ps); err != nil {
		t.Fatal(err)
	}
	got, err := loadProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	// Loaded profiles are sorted by label.
	if len(got) != 2 || got[0].ID != "1" || got[1].ID != "2" {
		t.Fatalf("profiles %+v", got)
	}
	if !got[0].UseTLS {
		t.Fatal("TLS flag lost")
	}
	// The file is private. Windows only tracks the read-only flag, not
	// Unix permission bits.
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode())
		}
	}
}

func TestSaveAndDeleteProfile(t *testing.T) {
	clear(fakeSecrets)
	a := newApp()
	a.profilesPath = filepath.Join(t.TempDir(), "profiles.json")
	a.form.reset()
	a.form.name, a.form.addr = "local", "127.0.0.1:6379"
	a.form.password = "pw"
	a.saveProfile()
	if a.connErr.key != "" || len(a.profiles) != 1 || a.selProfile == "" {
		t.Fatalf("err %q profiles %+v sel %q", a.connErr, a.profiles, a.selProfile)
	}
	id := a.selProfile
	if fakeSecrets["profile/"+id+"/redis"] != "pw" {
		t.Fatalf("password not stored: %v", fakeSecrets)
	}
	a.saveProfile() // upsert, not duplicate
	if len(a.profiles) != 1 {
		t.Fatalf("profiles %+v", a.profiles)
	}
	a.deleteProfile(id)
	if len(a.profiles) != 0 || a.selProfile != "" {
		t.Fatalf("profiles %+v sel %q", a.profiles, a.selProfile)
	}
	if _, ok := fakeSecrets["profile/"+id+"/redis"]; ok {
		t.Fatal("password left in the secret store")
	}
}

func TestSaveProfileStoresSecrets(t *testing.T) {
	clear(fakeSecrets)
	a := newApp()
	a.profilesPath = filepath.Join(t.TempDir(), "profiles.json")
	a.form.reset()
	a.form.addr = "127.0.0.1:6379"
	a.form.password = "s3cret"
	a.form.ssh = true
	a.form.sshAddr = "bastion:22"
	a.form.sshPassword = "tunnel"
	a.saveProfile()
	if a.connErr.key != "" {
		t.Fatal(a.connErr)
	}
	id := a.selProfile
	if fakeSecrets["profile/"+id+"/redis"] != "s3cret" || fakeSecrets["profile/"+id+"/ssh"] != "tunnel" {
		t.Fatalf("secrets %v", fakeSecrets)
	}
	// Clearing a password removes the stored secret instead of keeping it.
	a.form.password, a.form.sshPassword = "", ""
	a.form.clearRedis, a.form.ssh = true, false
	a.saveProfile()
	if _, ok := fakeSecrets["profile/"+id+"/redis"]; ok {
		t.Fatal("empty password kept")
	}
	if _, ok := fakeSecrets["profile/"+id+"/ssh"]; ok {
		t.Fatal("empty SSH password kept")
	}
}

func TestConnectedToSavesProfileOnce(t *testing.T) {
	clear(fakeSecrets)
	a := newApp()
	a.profilesPath = filepath.Join(t.TempDir(), "profiles.json")
	p := Profile{ID: "p1", Name: "local", Addr: "127.0.0.1:6379"}
	a.connectedTo(p, Store{}, "Redis 7.2.4 · standalone", SecretSet{Redis: "pw"})
	if a.selProfile != p.ID || len(a.profiles) != 1 || a.cur() == nil || a.cur().id != p.ID {
		t.Fatalf("sel %q profiles %+v cur %+v", a.selProfile, a.profiles, a.cur())
	}
	if fakeSecrets["profile/p1/redis"] != "pw" {
		t.Fatalf("secrets %v", fakeSecrets)
	}
	// Connecting the same profile again updates it instead of adding another.
	a.connectedTo(p, Store{}, "", SecretSet{Redis: "pw"})
	if len(a.profiles) != 1 || len(a.sessions) != 1 {
		t.Fatalf("profiles %+v sessions %d", a.profiles, len(a.sessions))
	}
}

func TestMainViewKeyBrowser(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	for _, k := range []string{"user:1", "user:2", "sess:a"} {
		s.keys.add(k)
	}
	tt := ui.NewTester(a.view, 1100, 700)
	for _, want := range []string{"local", "user", "sess", "3 keys", "Select a key"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestValuePanelHash(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.valKey = "h"
	s.val = Value{
		Type: "hash", TTL: 90 * time.Second, Size: 2,
		Pairs: [][2]string{{"f1", "v1"}, {"f2", "v2"}},
	}
	tt := ui.NewTester(a.view, 1100, 700)
	for _, want := range []string{"HASH", "h", "Field", "Value", "f1", "v2", "TTL 1m 30s"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestValuePanelString(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.valKey = "s"
	s.val = Value{Type: "string", TTL: -1, Str: "hello", Size: 5}
	s.strEditor.Buffer = nil
	tt := ui.NewTester(a.view, 1100, 700)
	if !tt.HasText("STRING") || !tt.HasText("Save") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestFormatTTL(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second:            "45s",
		90 * time.Second:            "1m 30s",
		2*time.Hour + 3*time.Minute: "2h 3m",
		26 * time.Hour:              "1d 2h",
	}
	for d, want := range cases {
		if got := formatTTL(d); got != want {
			t.Errorf("formatTTL(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestClickingConnectionFillsForm(t *testing.T) {
	clear(fakeSecrets)
	a := newApp()
	a.profiles = []Profile{
		{ID: "p1", Name: "First", Addr: "first:6379"},
		{ID: "p2", Name: "Second", Addr: "second:6380"},
	}
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("Second"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.selProfile != "p2" || a.form.name != "Second" || a.form.addr != "second:6380" {
		t.Fatalf("sel %q form %+v", a.selProfile, a.form)
	}
}

func TestFramedWindow(t *testing.T) {
	o := framed(mygo.WindowOptions{})
	if o.TitleBarStyle == "" || o.TitleBarHeight != topBarH {
		t.Fatalf("title bar: %+v", o)
	}
	if runtime.GOOS == "darwin" {
		if o.TitleBarStyle != mygo.TitleBarHiddenInset || o.TrafficLightPosition == nil {
			t.Fatalf("macOS chrome: %+v", o)
		}
	} else if o.TitleBarStyle != mygo.TitleBarHidden || !o.HideWindowButtons {
		t.Fatalf("other chrome: %+v", o)
	}
}

func TestNewConnectionClearsSelectedProfile(t *testing.T) {
	clear(fakeSecrets)
	a := newApp()
	a.profiles = []Profile{{ID: "p1", Name: "Prod", Addr: "redis.example:6380", UseTLS: true}}
	a.connSel = 0

	tt := ui.NewTester(a.view, 1180, 760)
	if a.selProfile != "p1" || a.form.addr != "redis.example:6380" {
		t.Fatalf("selected %q form %+v", a.selProfile, a.form)
	}
	if err := tt.Click("New Connection"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.selProfile != "" || a.form.name != "" || a.form.host != "127.0.0.1" || a.form.port != "6379" {
		t.Fatalf("new form: sel %q form %+v", a.selProfile, a.form)
	}
}

func TestConnectionBadges(t *testing.T) {
	a := newApp()
	a.profiles = []Profile{{
		ID: "p1", Name: "Prod", Addr: "redis.example:6380", UseTLS: true,
		SSH: SSHConfig{Enabled: true, Addr: "bastion:22", User: "ops"},
	}}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Prod", "TLS", "SSH", "redis.example:6380"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestDarkLayout(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	s.keys.add("user:1")
	tt := ui.NewTester(a.view, 1100, 700)
	tt.SetDark(true)
	for _, want := range []string{"local", "Select a key"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestValuePanelNone(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.valKey = "gone"
	s.val = Value{Type: "none"}
	tt := ui.NewTester(a.view, 1100, 700)
	if !tt.HasText("no longer exists") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestSessionTabs(t *testing.T) {
	a := newApp()
	s1 := newSession(a, Profile{ID: "a", Name: "Alpha", Addr: "a:1"}, Store{cfg: ConnConfig{Name: "Alpha", Addr: "a:1"}}, "")
	s2 := newSession(a, Profile{ID: "b", Name: "Beta", Addr: "b:2"}, Store{cfg: ConnConfig{Name: "Beta", Addr: "b:2"}}, "")
	a.sessions = []*session{s1, s2}
	a.active = 0
	s1.keys.add("alpha:1")
	s2.keys.add("beta:1")

	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Alpha", "Beta", "alpha", "Select a key"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Beta"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.active != 1 {
		t.Fatalf("active %d", a.active)
	}
	if err := tt.Click("Close Beta"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.sessions) != 1 || a.active != 0 {
		t.Fatalf("sessions %d active %d", len(a.sessions), a.active)
	}
}

func TestScanControls(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	s.keys.add("user:1")
	s.scanIncomplete, s.scanning = true, true
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"1 key", "Cancel scan", "scanning…"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Cancel scan"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.scanning || !s.scanIncomplete || s.keys.count != 1 || !tt.HasText("Scan incomplete") {
		t.Fatal("cancel lost loaded keys or reported a complete scan")
	}
	a.sessionMenuOpen = true
	tt.Frame()
	for _, label := range []string{"Load more keys", "Load all keys", "Load all matches (full search)"} {
		if tt.HasText(label) {
			t.Fatalf("obsolete scan control: %s", label)
		}
		for _, cmd := range a.paletteCommands() {
			if cmd.title == label {
				t.Fatalf("obsolete palette command: %s", label)
			}
		}
	}
}

func TestDatabaseSelector(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	s.cfg.DBAliases = map[int]string{1: "Sessions (active) · team"}
	s.dbChoices = nil
	for db := 0; db < 16; db++ {
		s.dbChoices = append(s.dbChoices, s.cfg.dbLabel(db))
	}
	s.dbChoice = "db0"
	s.keys.add("user:1")
	tt := ui.NewTester(a.view, 760, 520)
	if !tt.HasText("db0 (?)") {
		t.Fatal("unavailable statistics were shown as zero")
	}
	s.dbKeys = map[int]int64{0: 12, 1: 1234567}
	tt.Frame()
	if err := tt.Click("Database"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DB", "db0 (12)", "db1 (1234567) · Sessions (active) · team", "db2 (0)"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	// Live statistics change labels without changing the selected database ID.
	s.dbKeys[0] = 15
	tt.Frame()
	if !tt.HasText("db0 (15)") || s.dbChoice != "db0" || s.db != 0 {
		t.Fatal("count refresh changed the selected database")
	}
	tt.Key(0, ui.KeyEnd)
	tt.Frame()
	if r, ok := tt.Find("db15 (0)"); !ok || r.H <= 0 || r.Y < 0 || r.Y+r.H > 520 {
		t.Fatalf("last database is not reachable in a compact window: %+v", r)
	}
	tt.Key(0, ui.KeyEscape)
	s.dbKeys = nil
	tt.Frame()
	if !tt.HasText("db0 (?)") || s.dbChoice != "db0" {
		t.Fatal("unavailable statistics kept an outdated count")
	}
}

func TestRenameDialog(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	s.keys.add("user:1")
	a.openRename(s, "user:1")
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Rename key", "New name", "Rename", "Cancel"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestTTLDialog(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	s.keys.add("user:1")
	a.openTTL(s, []string{"user:1"})
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Set TTL", "Expire after (seconds)", "0 removes the expiration"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestNewKeyDialog(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	a.openNewKey(s)
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"New key", "Key", "Type", "TTL (seconds)", "Value", "Create", "Cancel"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestSessionMenu(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	s.keys.add("user:1")
	a.sessionMenuOpen = true
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"New key…", "Set TTL…", "Flush database…", "Disconnect"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestBatchSelectionBar(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	s.keys.add("user:1")
	s.keys.add("user:2")
	s.sel.Add("user:1")
	s.sel.Add("user:2")
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"2 selected", "Set TTL", "Delete selected", "Clear selection"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestParseNewValue(t *testing.T) {
	if v, err := parseNewValue("string", "a\nb"); err != nil || v.String != "a\nb" {
		t.Fatalf("string %+v %v", v, err)
	}
	if v, err := parseNewValue("hash", "a=1\nb=2"); err != nil || v.Fields["b"] != "2" {
		t.Fatalf("hash %+v %v", v, err)
	}
	if v, err := parseNewValue("zset", "m=1.5"); err != nil || v.Scores["m"] != 1.5 {
		t.Fatalf("zset %+v %v", v, err)
	}
	if _, err := parseNewValue("hash", "broken"); err == nil {
		t.Fatal("bad hash line accepted")
	}
	if _, err := parseNewValue("stream", "x"); err == nil {
		t.Fatal("unsupported type accepted")
	}
}

func TestBatchSelectionKeepsValue(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.keys.add("user:1")
	s.keys.add("user:2")
	s.valKey = "user:1"
	s.val = Value{Type: "string", Str: "one"}
	s.sel.Add("user:1")
	s.sel.Add("user:2")
	ui.NewTester(a.view, 1180, 760)
	if s.valKey != "user:1" {
		t.Fatalf("value jumped to %q", s.valKey)
	}
}

func TestHomeWithSessions(t *testing.T) {
	a := newApp()
	s := newSession(a, Profile{ID: "a", Name: "Alpha", Addr: "a:1"}, Store{cfg: ConnConfig{Name: "Alpha", Addr: "a:1"}}, "")
	a.sessions = []*session{s}
	a.active = -1
	a.profiles = []Profile{{ID: "p1", Name: "Prod", Addr: "prod:6379"}}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Connections", "Alpha", "Prod"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Alpha"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.active != 0 {
		t.Fatalf("active %d", a.active)
	}
	if err := tt.Click("New Connection"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.active != -1 {
		t.Fatalf("active after new %d", a.active)
	}
}

func TestFlushConfirmation(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	a.askFlush(s)
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Flush db0?", "Every key in this database", "Cancel", "Flush"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestBatchDeleteConfirmation(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	a.askDeleteKeys(s, []string{"user:1", "user:2"})
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("Delete 2 keys?") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestValueToolbar(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	s.valKey = "h"
	s.val = Value{Type: "hash", Size: 4, Pairs: [][2]string{{"a", "1"}}}
	s.valMore = true
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Format", "Decode", "Add row", "Load more rows"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestFormattedString(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	s.valKey = "s"
	s.val = Value{Type: "string", Str: `{"a":1}`, Size: 7}
	s.valFormat = "JSON"
	s.decodedEditor.Buffer = editor.NewBuffer(`{"a":1}`)
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("Decoded view") || !tt.HasText("Save") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestDecodedCell(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	s.keys.add("h")
	s.sel.Add("h")
	s.valKey = "h"
	s.val = Value{Type: "hash", Size: 1, Pairs: [][2]string{{"a", "aGVsbG8="}}}
	s.valDecode = "Base64"
	tt := ui.NewTester(a.view, 1180, 760)
	defer s.cellCancel()
	if !s.cellPending["aGVsbG8="] || !tt.HasText("…") {
		t.Fatal("decoded cell did not queue background work")
	}
	// The headless tester has no native Window.Update queue; apply the worker
	// result explicitly and check that the next frame displays it.
	codec := s.valueCodec()
	decoded, err := formatValueCustom("aGVsbG8=", codec.format, codec.decode, codec.decoder)
	if err != nil {
		t.Fatal(err)
	}
	s.cellCache["aGVsbG8="] = decoded
	tt.Frame()
	if !tt.HasText("hello") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestRowEditDialog(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	s.valKey = "h"
	s.val = Value{Type: "hash", Size: 1, Pairs: [][2]string{{"field", "value"}}}
	a.openRowEdit(s, 0)
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Edit field", "Field", "Value", "Save", "Cancel"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestRowAddDialog(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	s.valKey = "s"
	s.val = Value{Type: "set", Size: 0}
	a.openRowAdd(s)
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Add member", "Member", "Save", "Cancel"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestRowDeleteConfirmation(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	s.valKey = "h"
	s.val = Value{Type: "hash", Size: 1, Pairs: [][2]string{{"field", "value"}}}
	a.askDeleteRow(s, 0)
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Delete field", "field", "Cancel", "Delete"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestZSetValue(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	s.valKey = "z"
	s.val = Value{Type: "zset", Size: 2, Pairs: [][2]string{{"ada", "10"}, {"bob", "7.5"}}}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"ada", "10", "bob", "7.5"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestApplyValuePageLoadsString(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.applyValuePage(ValuePage{Value: Value{Type: "string", Str: `{"a":1}`, Size: 7}}, false)
	if s.strEditor.Buffer == nil || s.strEditor.Buffer.String() != `{"a":1}` {
		t.Fatalf("buffer %v", s.strEditor.Buffer)
	}
	s.applyValuePage(ValuePage{Value: Value{Type: "string", Str: "next", Size: 4}}, false)
	if s.strEditor.Buffer.String() != "next" {
		t.Fatalf("buffer after reload %q", s.strEditor.Buffer.String())
	}
}

func TestListFilterIndices(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.valKey = "l"
	s.val = Value{Type: "list", Size: 2, Items: []string{"a", "b"}}
	s.valIndices = []int{4, 7}
	if r := a.rowAt(s, 1); r.index != 7 {
		t.Fatalf("index %d", r.index)
	}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Add row", "4", "7"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestValuePageRemovesVanishedKey(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.keys.add("user:1")
	s.sel.Add("user:1")
	s.valKey = "user:1"
	s.applyValuePage(ValuePage{Value: Value{Type: "none"}}, false)
	if n := s.keys.node("user:1"); n != nil && n.isKey {
		t.Fatal("key still in tree")
	}
	if s.sel.Has("user:1") {
		t.Fatal("key still selected")
	}
	if s.status.key == "" {
		t.Fatal("no status")
	}
}

func TestToolTabs(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Name: "local", Addr: "127.0.0.1:6379"})
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Browser", "Status", "CLI", "Slow log", "Monitor", "Pub/Sub", "Cmd log"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Status"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.tool != "status" {
		t.Fatalf("tool %q", s.tool)
	}
	if err := tt.Click("Browser"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.tool != "browser" {
		t.Fatalf("tool %q", s.tool)
	}
}

func TestStatusView(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.tool = "status"
	s.statusInfo = parseInfo("# Server\nredis_version:7.2.4\nredis_mode:standalone\n# Keyspace\ndb0:keys=3,expires=1\ndb10:keys=12000,expires=15\ndb2:keys=26,expires=0\n")
	s.statusUpdated = time.Now()
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Server status", "Server information", "7.2.4", "standalone", "Database key statistics", "Key count", "With expiration", "db0", "3", "12,000"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	second, _ := tt.Find("db2")
	tenth, _ := tt.Find("db10")
	if second.Y >= tenth.Y {
		t.Fatal("database statistics are not in numeric order")
	}
	for i := 0; i < maxStatusHistory; i++ {
		s.addStatusSample()
		tt.Frame()
	}
	if tt.FramePending() || s.statusSampling || s.statusTimer != nil {
		t.Fatal("static status charts kept rendering or started background sampling")
	}
}

func TestCLIView(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.tool = "cli"
	s.cliEntries = []cliEntry{{command: "SET a 1", result: "OK"}}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"CLI", "SET a 1", "OK", "Run"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestSlowLogView(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.tool = "slowlog"
	s.slowEntries = []slowEntry{{at: time.Now(), duration: 12 * time.Millisecond, command: "GET big"}}
	s.slowTotal = 1
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Slow log", "GET big", "12.0 ms", "1 entry"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestMonitorView(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.tool = "monitor"
	s.monitorRunning = true
	s.monitorLines = []string{`1700000000.000 [0 127.0.0.1] "GET" "k"`}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Monitor", "GET", "k"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestPubSubView(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.tool = "pubsub"
	s.pubMessages = []pubMessage{{at: time.Now(), channel: "news", payload: "hello"}}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Pub/Sub", "news", "hello"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestCommandLogView(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.tool = "cmdlog"
	s.cmdLog = []CommandEntry{{At: time.Now(), Name: "get", Args: []string{"k"}, Dur: time.Millisecond}}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Command log", "GET k", "1 command"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestSwitchingTabsStopsLiveTools(t *testing.T) {
	a := newApp()
	s1 := newSession(a, Profile{ID: "a", Name: "A", Addr: "a:1"}, Store{cfg: ConnConfig{Name: "A", Addr: "a:1"}}, "")
	s2 := newSession(a, Profile{ID: "b", Name: "B", Addr: "b:1"}, Store{cfg: ConnConfig{Name: "B", Addr: "b:1"}}, "")
	a.sessions = []*session{s1, s2}
	a.active = 0
	s1.tool = "monitor"
	s1.monitorRunning = true
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("B"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s1.monitorRunning {
		t.Fatal("monitor kept running on a hidden session")
	}
	if a.active != 1 {
		t.Fatalf("active %d", a.active)
	}
}

func TestSlowResetConfirmation(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	a.askSlowReset(s)
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Reset slow log?", "Every slow log entry", "Cancel", "Reset"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestStatusBar(t *testing.T) {
	a := newApp()
	a.setStatus("could not save")
	a.statusErr = true
	tt := ui.NewTester(a.view, 900, 600)
	if !tt.HasText("could not save") {
		t.Fatalf("status missing in %q", tt.Texts())
	}
	if err := tt.Click("Dismiss"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.status.key != "" || a.statusErr {
		t.Fatalf("status %q err %v", a.status, a.statusErr)
	}
}

func TestBranchSelectionClearsValue(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.keys.add("user:1")
	s.valKey = "user:1"
	s.val = Value{Type: "string", Str: "old"}
	s.sel.Add("user")

	ui.NewTester(a.view, 1100, 700)

	if s.valKey != "" || s.val.Type != "" {
		t.Fatalf("value panel kept %q/%q", s.valKey, s.val.Type)
	}
}

func TestScanBatchGeneration(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.scanID = 7
	s.applyScanBatch(6, []string{"old:1"})
	if s.keys.node("old:1") != nil {
		t.Fatal("a stale batch leaked into the new scan")
	}
	s.applyScanBatch(7, []string{"new:1"})
	if s.keys.node("new:1") == nil {
		t.Fatal("the current batch was dropped")
	}
	if got := scanPattern(" user "); got != "*user*" {
		t.Fatalf("pattern %q", got)
	}
}

func TestDeleteConfirmation(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.keys.add("user:1")
	a.askDeleteNamespace(s, "user")

	tt := ui.NewTester(a.view, 1100, 700)
	for _, want := range []string{"Delete namespace", "Every key", "Cancel"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.confirmOpen || a.pending.path != "" {
		t.Fatalf("confirmation still pending: %v %q", a.confirmOpen, a.pending.path)
	}
}

func TestProfileSecretsAreLazyAndCached(t *testing.T) {
	clear(fakeSecrets)
	fakeSecrets["profile/p1/redis"] = "pw"
	calls := 0
	orig := secretGet
	secretGet = func(key string) ([]byte, error) {
		calls++
		return orig(key)
	}
	t.Cleanup(func() { secretGet = orig })
	a := newApp()
	p := Profile{ID: "p1", Addr: "127.0.0.1:6379"}
	a.form.fill(p)
	if calls != 0 {
		t.Fatalf("filling the form read secrets %d times", calls)
	}
	s, err := a.profileSecrets(p)
	if err != nil || s.Redis != "pw" || calls != 1 {
		t.Fatalf("secrets %+v err %v calls %d", s, err, calls)
	}
	if _, err := a.profileSecrets(p); err != nil || calls != 1 {
		t.Fatalf("cache miss err %v calls %d", err, calls)
	}
}

func TestConnectSwitchesToOpenSession(t *testing.T) {
	clear(fakeSecrets)
	fakeSecrets["profile/test/redis"] = "pw"
	calls := 0
	orig := secretGet
	secretGet = func(key string) ([]byte, error) {
		calls++
		return orig(key)
	}
	t.Cleanup(func() { secretGet = orig })
	a := newApp()
	testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	a.active = -1
	a.selProfile = "test"
	a.form.fill(Profile{ID: "test", Addr: "127.0.0.1:6379"})
	a.connectSelected()
	if a.active != 0 || calls != 0 {
		t.Fatalf("active %d secret reads %d", a.active, calls)
	}
}

func TestLastSessionConfig(t *testing.T) {
	clear(fakeSecrets)
	fakeSecrets["profile/p1/redis"] = "pw"
	a := newApp()
	a.prefs.Startup, a.prefs.LastProfile, a.prefs.LastDB = "Last session", "p1", 3
	a.profiles = []Profile{{ID: "p1", Name: "Prod", Addr: "redis:6379", DB: 5}}
	p, cfg, ok, err := a.lastSessionConfig()
	if err != nil || !ok || p.ID != "p1" || cfg.DB != 3 || cfg.Password != "pw" {
		t.Fatalf("last config %+v %+v %v %v", p, cfg, ok, err)
	}
	a.prefs.LastDB = 0
	if _, cfg, ok, err = a.lastSessionConfig(); err != nil || !ok || cfg.DB != 0 {
		t.Fatalf("db0 config %+v %v %v", cfg, ok, err)
	}
	// Cluster has only db0.
	a.profiles[0].Mode = "cluster"
	a.profiles[0].ClusterAddrs = []string{"c:7000"}
	_, cfg, ok, err = a.lastSessionConfig()
	if err != nil || !ok || cfg.DB != 0 {
		t.Fatalf("cluster config %+v %v %v", cfg, ok, err)
	}
}

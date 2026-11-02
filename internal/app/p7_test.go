package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{12: "12 B", 1536: "1.5 KB", 2 << 20: "2.0 MB", 3 << 30: "3.0 GB"}
	for n, want := range cases {
		if got := formatBytes(n); got != want {
			t.Fatalf("%d -> %q", n, got)
		}
	}
}

func TestValueMetaAndText(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.val = Value{Type: "hash", Pairs: [][2]string{{"a", "1"}, {"b", "2"}}, Memory: 1536}
	if got := s.valMeta(); !strings.Contains(got, "2 fields") || !strings.Contains(got, "1.5 KB") {
		t.Fatalf("meta %q", got)
	}
	if got := s.valueText(); !strings.Contains(got, `"a": "1"`) {
		t.Fatalf("text %q", got)
	}
	s.val = Value{Type: "list", Items: []string{"x", "y"}}
	if got := s.valueText(); !strings.Contains(got, `"x"`) {
		t.Fatalf("list text %q", got)
	}
	s.strEditor.Buffer = nil
	s.val = Value{Type: "string", Str: "hello"}
	if got := s.valueText(); got != "hello" {
		t.Fatalf("string text %q", got)
	}
}

func TestCheckModeClick(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.keys.add("alpha")
	s.keys.add("beta")
	s.toggleCheckMode()
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("alpha"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !s.sel.Has("alpha") {
		t.Fatalf("selection %v", s.sel.All())
	}
	if !tt.HasText("1 selected") {
		t.Fatalf("batch bar missing: %q", tt.Texts())
	}
	if err := tt.Click("alpha"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.sel.Has("alpha") {
		t.Fatal("second click did not uncheck")
	}
}

func TestAutoRefreshToggle(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.toggleAutoRefresh()
	if !s.autoRefresh || s.autoTimer == nil {
		t.Fatalf("auto refresh not started: %v", s.autoRefresh)
	}
	s.toggleAutoRefresh()
	if s.autoRefresh || s.autoTimer != nil {
		t.Fatal("auto refresh not stopped")
	}
}

func TestBinaryKeyProtection(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	key := "bad\xffkey"
	s.keys.add(key)
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("BIN") {
		t.Fatalf("binary badge missing: %q", tt.Texts())
	}
	a.openRename(s, key)
	if a.renameOpen || s.status.key == "" {
		t.Fatalf("rename %v status %q", a.renameOpen, s.status)
	}
}

func TestValueFullscreenToggle(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.keys.add("k")
	s.sel.Add("k")
	s.valKey = "k"
	s.val = Value{Type: "string", Str: "x"}
	s.strEditor.Buffer = nil
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("Toggle value fullscreen"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !s.valueFull {
		t.Fatal("fullscreen did not turn on")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if s.valueFull {
		t.Fatal("Escape did not leave fullscreen")
	}
}

func TestClickKeySelectsAndLoadsValue(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.keys.add("alpha")
	s.keys.add("beta")
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("alpha"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.valKey != "alpha" || !s.sel.Has("alpha") {
		t.Fatalf("after click val %q sel %v", s.valKey, s.sel.All())
	}
	if err := tt.Click("beta"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.valKey != "beta" || s.sel.Has("alpha") || !s.sel.Has("beta") {
		t.Fatalf("after second click val %q sel %v", s.valKey, s.sel.All())
	}
}

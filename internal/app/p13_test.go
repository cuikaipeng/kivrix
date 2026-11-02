package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestConnectionKeyboardNavigation(t *testing.T) {
	a := newApp()
	a.profiles = []Profile{
		{ID: "a", Name: "A", Addr: "a:6379"},
		{ID: "b", Name: "B", Addr: "b:6379"},
	}
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Key(ui.Cmd, ui.KeyDown)
	tt.Frame()
	if a.connSel != 0 || a.selProfile != "a" {
		t.Fatalf("down selected %d %q", a.connSel, a.selProfile)
	}
	tt.Key(ui.Cmd, ui.KeyDown)
	tt.Frame()
	if a.connSel != 1 || a.selProfile != "b" {
		t.Fatalf("down2 selected %d %q", a.connSel, a.selProfile)
	}
	tt.Key(ui.Cmd, ui.KeyUp)
	tt.Frame()
	if a.connSel != 0 {
		t.Fatalf("up selected %d", a.connSel)
	}
}

func TestSessionTabKeyboardCycle(t *testing.T) {
	a := newApp()
	s1 := newSession(a, Profile{ID: "1", Name: "A"}, Store{cfg: ConnConfig{Name: "A", Addr: "a:6379"}}, "")
	s2 := newSession(a, Profile{ID: "2", Name: "B"}, Store{cfg: ConnConfig{Name: "B", Addr: "b:6379"}}, "")
	a.sessions = []*session{s1, s2}
	a.active = 0
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Key(ui.Ctrl, ui.KeyTab)
	tt.Frame()
	if a.active != 1 {
		t.Fatalf("next tab %d", a.active)
	}
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyTab)
	tt.Frame()
	if a.active != 0 {
		t.Fatalf("prev tab %d", a.active)
	}
}

func TestPinRowEditor(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.valKey = "user:1"
	s.val = Value{Type: "hash", Pairs: [][2]string{{"name", "Ada"}}}
	a.rowSessionID = s.id
	a.rowOpen = true
	a.rowPinned = false
	a.row = rowEdit{key: "user:1", typ: "hash", mode: "edit", field: "name", oldField: "name", value: "Ada"}
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("Pin editor") {
		t.Fatalf("pin label missing: %q", tt.Texts())
	}
	if err := tt.Click("Pin editor"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.rowPinned {
		t.Fatal("pin did not toggle")
	}
}

func TestConnectionKeyboardOpensGroup(t *testing.T) {
	a := newApp()
	a.profiles = []Profile{{ID: "a", Name: "A", Group: "G", Addr: "a:6379"}}
	a.connCollapsed["G"] = true
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Key(ui.Cmd, ui.KeyDown)
	tt.Frame()
	if a.connSel != 0 || a.connCollapsed["G"] {
		t.Fatalf("selection %d collapsed %v", a.connSel, a.connCollapsed["G"])
	}
}

func TestFormattedViewEditRaw(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.keys.add("k")
	s.sel.Add("k")
	s.valKey = "k"
	s.val = Value{Type: "string", Str: "hello"}
	s.valFormat = "JSON"
	s.valDecode = "Base64"
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("Edit raw"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.valFormat != "Raw" || s.valDecode != "None" {
		t.Fatalf("format %q decode %q", s.valFormat, s.valDecode)
	}
}

func TestEditSelectedRowButton(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.keys.add("h")
	s.sel.Add("h")
	s.valKey = "h"
	s.val = Value{Type: "hash", Pairs: [][2]string{{"name", "Ada"}}}
	s.valRowSel = 0
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("Edit selected row"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.rowOpen || a.row.field != "name" {
		t.Fatalf("row open %v row %+v", a.rowOpen, a.row)
	}
}

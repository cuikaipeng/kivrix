package app

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestStreamStore(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	if err := st.CreateKey(ctx, "events", "stream", NewKeyValue{Stream: []StreamEntry{{Fields: [][2]string{{"kind", "start"}}}}}, 0); err != nil {
		t.Fatal(err)
	}
	id, err := st.StreamAdd(ctx, "events", "*", [][2]string{{"kind", "stop"}, {"by", "ada"}})
	if err != nil || id == "" {
		t.Fatalf("add %q %v", id, err)
	}
	p, err := st.GetValuePage(ctx, "events", 0, 0, 10, "")
	if err != nil || p.Type != "stream" || len(p.Stream) != 2 || p.Size != 2 {
		t.Fatalf("page %+v %v", p, err)
	}
	if p.Stream[0].ID == "" || len(p.Stream[0].Fields) == 0 {
		t.Fatalf("entry %+v", p.Stream[0])
	}
	if err := st.StreamDel(ctx, "events", id); err != nil {
		t.Fatal(err)
	}
	p, _ = st.GetValuePage(ctx, "events", 0, 0, 10, "")
	if len(p.Stream) != 1 || p.Size != 1 {
		t.Fatalf("after delete %+v", p)
	}
}

func TestJSONValueParsing(t *testing.T) {
	if _, err := parseNewValue("json", `{"a":1}`); err != nil {
		t.Fatal(err)
	}
	if _, err := parseNewValue("json", `not json`); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestStreamView(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.valKey = "events"
	s.val = Value{Type: "stream", Size: 2, LastID: "1700-2", Stream: []StreamEntry{
		{ID: "1700-2", Fields: [][2]string{{"kind", "stop"}}},
		{ID: "1700-1", Fields: [][2]string{{"kind", "start"}}},
	}}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"STREAM", "1700-2", "kind=stop", "Add row"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestStreamAddDialog(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.valKey = "events"
	s.val = Value{Type: "stream", Size: 1, Stream: []StreamEntry{{ID: "1-1", Fields: [][2]string{{"a", "b"}}}}}
	a.openRowAdd(s)
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Add entry", "Entry ID", "Fields", "field=value"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestJSONView(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.valKey = "doc"
	s.keys.add("doc")
	s.sel.Add("doc")
	s.applyValuePage(ValuePage{Value: Value{Type: "json", TTL: -1, Str: `{"name":"Ada"}`, Size: 14}}, false)
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"JSON", "doc", "Save"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	s.valFormat = "JSON"
	s.decodedEditor.Buffer = editor.NewBuffer(`{"name":"Ada"}`)
	tt.Frame()
	if !tt.HasText("Decoded view") || !tt.HasText("Save") {
		t.Fatalf("formatted texts %q", tt.Texts())
	}
}

func TestCSVMenu(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.keys.add("user:1")
	s.keys.add("user:2")
	s.sel.Add("user:1")
	s.sel.Add("user:2")
	a.sessionMenuOpen = true
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Export selected keys…", "Import CSV…"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

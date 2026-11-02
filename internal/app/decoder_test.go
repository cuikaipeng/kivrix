package app

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/egoist/mygo/ui/editor"
)

func TestLegacyDecoderMigration(t *testing.T) {
	p := defaultPreferences()
	p.CustomDecoder = "cat"
	p.DefaultDecode = "Custom"
	p.normalize()
	if p.CustomDecoder != "" || len(p.CustomDecoders) != 1 || p.CustomDecoders[0].Name != "Custom" {
		t.Fatalf("migrated %+v", p)
	}
	if p.DefaultDecode != "Custom: Custom" {
		t.Fatalf("decode %q", p.DefaultDecode)
	}
}

func TestNamedDecoderResolution(t *testing.T) {
	a := newApp()
	a.prefs.CustomDecoders = []CustomDecoder{{Name: "Cat", Command: "cat"}}
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.valDecode = "Custom: Cat"
	decoder, command := s.decodeCommand()
	if decoder != "Custom" || command != "cat" {
		t.Fatalf("decode %q command %q", decoder, command)
	}
	if !containsString(s.decodeChoices(), "Custom: Cat") {
		t.Fatalf("choices %v", s.decodeChoices())
	}
	s.valDecode = "Custom: Missing"
	if _, command := s.decodeCommand(); command != "" {
		t.Fatalf("missing command %q", command)
	}
}

func TestDecoderNamesAreUnique(t *testing.T) {
	p := defaultPreferences()
	p.CustomDecoders = []CustomDecoder{{Name: "A", Command: "cat"}, {Name: "A", Command: "cat"}, {Name: "", Command: "cat"}}
	p.normalize()
	if len(p.CustomDecoders) != 1 {
		t.Fatalf("decoders %+v", p.CustomDecoders)
	}
}

func TestDecoderEditor(t *testing.T) {
	a := newApp()
	a.openDecoderEditor(-1)
	a.decoderName, a.decoderCmd, a.decoderEncode = "Upper", "tr a-z A-Z", "tr A-Z a-z"
	a.saveDecoder()
	if len(a.prefs.CustomDecoders) != 1 || a.prefs.CustomDecoders[0].Name != "Upper" || a.prefs.CustomDecoders[0].EncodeCommand != "tr A-Z a-z" {
		t.Fatalf("decoders %+v", a.prefs.CustomDecoders)
	}
	a.prefs.DefaultDecode = "Custom: Upper"
	a.openDecoderEditor(0)
	a.decoderName = "Upper2"
	a.saveDecoder()
	if a.prefs.CustomDecoders[0].Name != "Upper2" || a.prefs.DefaultDecode != "Custom: Upper2" {
		t.Fatalf("renamed %+v default %q", a.prefs.CustomDecoders, a.prefs.DefaultDecode)
	}
	a.deleteDecoder(0)
	if len(a.prefs.CustomDecoders) != 0 || a.prefs.DefaultDecode != "None" {
		t.Fatalf("deleted %+v default %q", a.prefs.CustomDecoders, a.prefs.DefaultDecode)
	}
}

func TestEncodedEditorState(t *testing.T) {
	a := newApp()
	a.prefs.CustomDecoders = []CustomDecoder{{Name: "Cat", Command: "cat", EncodeCommand: "cat"}}
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.val = Value{Type: "string", Str: "hello"}
	s.valKey = "k"
	s.valDecode = "Custom: Cat"
	s.valFormat = "Raw"
	if !s.encodedEditorActive() {
		t.Fatal("encoded editor should be active")
	}
	if s.canSaveValue() {
		t.Fatal("save should wait for the decoded buffer")
	}
	s.decodedEditor.Buffer = editor.NewBuffer("hello")
	if !s.canSaveValue() {
		t.Fatal("save should be available with a buffer")
	}
}

func TestSaveEncodedString(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat is not available")
	}
	st, _ := testStore(t)
	a := newApp()
	a.prefs.CustomDecoders = []CustomDecoder{{Name: "Cat", Command: "cat", EncodeCommand: "cat"}}
	s := newSession(a, Profile{ID: "p", Name: "P"}, *st, "")
	a.sessions = []*session{s}
	a.active = 0
	s.valKey, s.val = "k", Value{Type: "string", Str: "old"}
	s.valDecode, s.valFormat = "Custom: Cat", "Raw"
	s.decodedEditor.Buffer = editor.NewBuffer("new")
	s.saveEncodedString()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v, err := st.GetValue(context.Background(), "k")
		if err == nil && v.Str == "new" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	v, _ := st.GetValue(context.Background(), "k")
	t.Fatalf("stored %q", v.Str)
}

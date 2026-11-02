package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestProfileGroupRoundTrip(t *testing.T) {
	var f connForm
	f.reset()
	f.name, f.group, f.addr = "Prod", "Production", "redis:6379"
	p := f.profile("id")
	if p.Group != "Production" {
		t.Fatalf("group %q", p.Group)
	}
	var g connForm
	g.fill(p)
	if g.group != "Production" {
		t.Fatalf("filled group %q", g.group)
	}
}

func TestConnectionGroupsAndFilter(t *testing.T) {
	a := newApp()
	a.profiles = []Profile{
		{ID: "p1", Name: "Prod Primary", Group: "Production", Addr: "p:6379"},
		{ID: "p2", Name: "Staging", Group: "Staging", Addr: "s:6379"},
		{ID: "p3", Name: "Local", Addr: "127.0.0.1:6379"},
	}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Production", "Staging", "Ungrouped", "Prod Primary", "Local"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	a.connQuery = "staging"
	tt.Frame()
	if !tt.HasText("Staging") || tt.HasText("Prod Primary") {
		t.Fatalf("filtered texts %q", tt.Texts())
	}
}

func TestImportProfileList(t *testing.T) {
	data, err := marshalProfiles([]Profile{
		{Name: "Imported", Group: "Team", Addr: "imported:6379"},
		{Name: "No address"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := decodeProfiles(data)
	if err != nil || len(ps) != 2 {
		t.Fatalf("decode %d %v", len(ps), err)
	}
	out, added, skipped := importProfileList([]Profile{{ID: "old", Name: "Old", Addr: "old:6379"}}, ps)
	if added != 1 || skipped != 1 || len(out) != 2 {
		t.Fatalf("added %d skipped %d out %d", added, skipped, len(out))
	}
	for _, p := range out {
		if p.Name == "Imported" && (p.ID == "" || p.ID == "old" || p.Group != "Team") {
			t.Fatalf("imported %+v", p)
		}
	}
	if strings.Contains(string(data), "password") {
		t.Fatal("profile export mentions passwords")
	}
}

func TestDuplicateProfileCopiesSecrets(t *testing.T) {
	clear(fakeSecrets)
	fakeSecrets["profile/src/redis"] = "secret"
	a := newApp()
	a.profiles = []Profile{{ID: "src", Name: "Prod", Group: "G", Addr: "redis:6379"}}
	a.duplicateProfile("src")
	if len(a.profiles) != 2 {
		t.Fatalf("profiles %d", len(a.profiles))
	}
	var dup Profile
	for _, p := range a.profiles {
		if p.ID != "src" {
			dup = p
		}
	}
	if dup.Name != "Prod copy" || dup.Group != "G" {
		t.Fatalf("duplicate %+v", dup)
	}
	if got := loadSecret("profile/" + dup.ID + "/redis"); got != "secret" {
		t.Fatalf("duplicate secret %q", got)
	}
	if a.selProfile != dup.ID {
		t.Fatalf("selected %q", a.selProfile)
	}
}

func TestGroupRenameAndDelete(t *testing.T) {
	a := newApp()
	a.profiles = []Profile{
		{ID: "1", Name: "A", Group: "Old", Addr: "a:6379"},
		{ID: "2", Name: "B", Group: "Old", Addr: "b:6379"},
	}
	a.renameGroup("Old", "New")
	for _, p := range a.profiles {
		if p.Group != "New" {
			t.Fatalf("group %q", p.Group)
		}
	}
	a.deleteGroup("New")
	for _, p := range a.profiles {
		if p.Group != "" {
			t.Fatalf("group after delete %q", p.Group)
		}
	}
}

func TestDisconnectAll(t *testing.T) {
	a := newApp()
	s1 := newSession(a, Profile{ID: "1", Name: "A"}, Store{cfg: ConnConfig{Name: "A", Addr: "a:6379"}}, "")
	s2 := newSession(a, Profile{ID: "2", Name: "B"}, Store{cfg: ConnConfig{Name: "B", Addr: "b:6379"}}, "")
	a.sessions = []*session{s1, s2}
	a.active = 0
	a.disconnectAll()
	if len(a.sessions) != 0 || a.active != -1 {
		t.Fatalf("sessions %d active %d", len(a.sessions), a.active)
	}
}

func TestGroupRenameModal(t *testing.T) {
	a := newApp()
	a.profiles = []Profile{{ID: "1", Name: "A", Group: "Old", Addr: "a:6379"}}
	a.openGroupRename("Old")
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("Rename group") || !tt.HasText("Group name") {
		t.Fatalf("modal texts %q", tt.Texts())
	}
}

func TestConnectionDeleteConfirmation(t *testing.T) {
	a := newApp()
	a.profiles = []Profile{{ID: "p1", Name: "Prod", Addr: "redis:6379"}}
	a.askDeleteProfile("p1")
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"Delete connection?", "Prod", "Cancel"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	if len(a.profiles) != 1 {
		t.Fatal("profile deleted before confirmation")
	}
}

func TestSplitJoinAddress(t *testing.T) {
	cases := []struct{ addr, host, port string }{
		{"redis.example:6380", "redis.example", "6380"},
		{"127.0.0.1", "127.0.0.1", "6379"},
		{"[::1]:6379", "::1", "6379"},
		{"", "", "6379"},
	}
	for _, c := range cases {
		h, p := splitAddress(c.addr)
		if h != c.host || p != c.port {
			t.Fatalf("%q => %q %q", c.addr, h, p)
		}
		if got := joinAddress(h, p); c.addr != "" && got != c.addr && c.addr != c.host {
			t.Fatalf("%q => %q", c.addr, got)
		}
	}
	var f connForm
	f.fill(Profile{Addr: "redis.example:6380"})
	if f.host != "redis.example" || f.port != "6380" || f.address() != "redis.example:6380" {
		t.Fatalf("form %+v address %q", f, f.address())
	}
}

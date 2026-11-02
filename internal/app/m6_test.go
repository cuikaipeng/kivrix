package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestPreferencesPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	p, err := loadPreferences(path)
	if err != nil || p.Theme != "System" || p.Language != "system" || p.DefaultFormat != "Raw" ||
		p.Background != "Petal" || !p.translucent() || p.BackgroundTransparency != 40 || p.Startup != "Connection list" {
		t.Fatalf("defaults %+v %v", p, err)
	}
	p.Theme, p.Language, p.DefaultFormat, p.DefaultDecode, p.CheckUpdates = "Dark", "简体中文", "JSON", "Base64", true
	p.Background, p.Vibrancy = "Meadow", boolPtr(false)
	p.BackgroundTransparency = 75
	p.Startup, p.LastProfile, p.LastDB = "Last session", "p1", 3
	p.CustomDecoders = []CustomDecoder{{Name: "Cat", Command: "cat"}}
	if err := savePreferences(path, p); err != nil {
		t.Fatal(err)
	}
	got, err := loadPreferences(path)
	if err != nil || got.Theme != "Dark" || got.Language != "zh-Hans" || got.DefaultFormat != "JSON" ||
		!got.CheckUpdates || got.Background != "Meadow" || got.translucent() || got.BackgroundTransparency != 75 ||
		got.Startup != "Last session" || got.LastProfile != "p1" || got.LastDB != 3 ||
		len(got.CustomDecoders) != 1 || got.CustomDecoders[0].Name != "Cat" {
		t.Fatalf("loaded %+v %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", info.Mode(), err)
	}
}

func TestBackgroundTransparencyMigration(t *testing.T) {
	for _, tc := range []struct {
		json string
		want float64
	}{
		{`{"vibrancy":true}`, 40}, // existing preferences without the new field
		{`{"backgroundTransparency":0}`, 0},
		{`{"backgroundTransparency":100}`, 100},
		{`{"backgroundTransparency":-1}`, 40},
		{`{"backgroundTransparency":101}`, 40},
	} {
		t.Run(tc.json, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "preferences.json")
			if err := os.WriteFile(path, []byte(tc.json), 0o600); err != nil {
				t.Fatal(err)
			}
			p, err := loadPreferences(path)
			if err != nil || p.BackgroundTransparency != tc.want {
				t.Fatalf("loaded transparency %g, want %g: %v", p.BackgroundTransparency, tc.want, err)
			}
			if err := savePreferences(path, p); err != nil {
				t.Fatal(err)
			}
			p, err = loadPreferences(path)
			if err != nil || p.BackgroundTransparency != tc.want {
				t.Fatalf("saved transparency %g, want %g: %v", p.BackgroundTransparency, tc.want, err)
			}
		})
	}
}

func TestBackgroundTransparencySlider(t *testing.T) {
	h := newUIFlow(t)
	h.a.prefsOpen, h.a.prefsTab = true, settingsAppearance
	h.ui = ui.NewTester(h.a.settingsView, 700, 480)
	h.ui.SetVibrancy(true)
	percent, ok := h.ui.Find("40%")
	if !ok {
		t.Fatal("missing transparency percentage")
	}
	// The track ends just before the percentage; the caption has the same
	// accessible name as the slider, so click the visible track directly.
	x, y := percent.X-24, percent.Y+percent.H/2
	h.ui.Press(x, y)
	h.ui.Move(x-50, y)
	h.ui.Release(x-50, y)
	h.ui.Key(0, ui.KeyEnd)
	h.ui.Key(0, ui.KeyLeft)
	for range 3 {
		h.ui.Frame()
	}
	if h.a.prefs.BackgroundTransparency != 99 || !h.ui.HasText("99%") {
		t.Fatalf("slider did not retain its keyboard edit: %g", h.a.prefs.BackgroundTransparency)
	}
	h.wait("transparency saved", func() bool { return h.a.prefsSaveTimer == nil })
	saved, err := loadPreferences(h.a.prefsPath)
	if err != nil || saved.BackgroundTransparency != 99 {
		t.Fatalf("saved transparency %g: %v", saved.BackgroundTransparency, err)
	}
	h.click("Translucent window")
	h.ui.ClickAt(x, y)
	h.ui.Key(0, ui.KeyHome)
	if h.a.prefs.BackgroundTransparency != 99 {
		t.Fatal("disabled slider changed the retained transparency")
	}
	h.click("Translucent window")
	h.a.prefsTab = settingsGeneral
	h.ui.Frame()
	h.a.prefsTab = settingsAppearance
	h.ui.Frame()
	if !h.ui.HasText("99%") {
		t.Fatal("returning to Appearance reset the transparency")
	}
	h.ui.ClickAt(x, y)
	h.ui.Key(0, ui.KeyHome)
	h.wait("zero transparency saved", func() bool { return h.a.prefsSaveTimer == nil })
	saved, err = loadPreferences(h.a.prefsPath)
	if err != nil || saved.BackgroundTransparency != 0 {
		t.Fatalf("saved zero transparency %g: %v", saved.BackgroundTransparency, err)
	}
	h.ui.Move(0, 0)
	h.ui.Advance(time.Second)
	if h.ui.FramePending() {
		t.Fatal("slider keeps repainting while idle")
	}
}

func TestWindowBackgroundTransparency(t *testing.T) {
	a := newApp()
	a.prefsOpen, a.prefsTab = true, settingsAppearance
	for _, view := range []struct {
		name string
		fn   func(*ui.Context)
	}{
		{"main", a.view}, {"settings", a.settingsView},
	} {
		t.Run(view.name, func(t *testing.T) {
			tt := ui.NewTester(view.fn, 820, 620)
			for _, dark := range []bool{false, true} {
				tt.SetDark(dark)
				for _, tc := range []struct {
					transparency float64
					material, on bool
					alpha        uint8
				}{
					{0, true, true, 255}, {40, true, true, 179}, {100, true, true, 64},
					{100, false, true, 255}, {100, true, false, 255},
				} {
					a.prefs.BackgroundTransparency, *a.prefs.Vibrancy = tc.transparency, tc.on
					tt.SetVibrancy(tc.material)
					img := tt.Image()
					if got := img.RGBAAt(2, 300).A; got != tc.alpha {
						t.Fatalf("dark=%t transparency=%g material=%t on=%t: background alpha=%d, want %d",
							dark, tc.transparency, tc.material, tc.on, got, tc.alpha)
					}
					if got := img.RGBAAt(810, 300).A; got != 255 {
						t.Fatalf("content card became transparent: alpha=%d", got)
					}
				}
			}
		})
	}
}

func TestPreferenceSwitchesPersist(t *testing.T) {
	for _, tc := range []struct {
		label string
		tab   int
		value func(*Preferences) *bool
	}{
		{"Check for updates on launch", settingsGeneral, func(p *Preferences) *bool { return &p.CheckUpdates }},
		{"Line numbers", settingsGeneral, func(p *Preferences) *bool { return p.LineNumbers }},
		{"Code folding", settingsGeneral, func(p *Preferences) *bool { return p.Folding }},
		{"Soft wrap", settingsGeneral, func(p *Preferences) *bool { return &p.SoftWrap }},
		{"Translucent window", settingsAppearance, func(p *Preferences) *bool { return p.Vibrancy }},
	} {
		t.Run(tc.label, func(t *testing.T) {
			a := newApp()
			a.prefsPath = filepath.Join(t.TempDir(), "preferences.json")
			a.prefsOpen, a.prefsTab = true, tc.tab
			tt := ui.NewTester(a.settingsView, 820, 1000)
			initial := *tc.value(&a.prefs)
			check := func(want bool) {
				t.Helper()
				for range 3 {
					tt.Frame()
				}
				saved, err := loadPreferences(a.prefsPath)
				if err != nil || *tc.value(&a.prefs) != want || *tc.value(&saved) != want {
					t.Fatalf("switch did not retain and save %t: %v", want, err)
				}
			}
			r, ok := tt.Find(tc.label)
			if !ok || r.H == 0 {
				t.Fatal("missing preference switch")
			}
			tt.ClickAt(r.X+8, r.Y+r.H/2) // the label
			check(!initial)
			tt.Key(0, ui.KeySpace)
			check(initial)
			tt.ClickAt(r.X+r.W-19, r.Y+r.H/2) // the switch track
			check(!initial)
			a.prefsTab = settingsShortcuts
			tt.Frame()
			a.prefsTab = tc.tab
			check(!initial)
		})
	}
}

func TestPreferencesDefaultsForNewSessions(t *testing.T) {
	a := newApp()
	a.prefs.DefaultFormat = "JSON"
	a.prefs.DefaultDecode = "Base64"
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	if s.valFormat != "JSON" || s.valDecode != "Base64" {
		t.Fatalf("format %q decode %q", s.valFormat, s.valDecode)
	}
}

func TestTranslations(t *testing.T) {
	a := newApp()
	a.prefs.Language = "en"
	if got := a.tr("Browser"); got != "Browser" {
		t.Fatalf("follow system should be English here: %q", got)
	}
	a.prefs.Language = "zh-Hans"
	if got := a.tr("Browser"); got != "浏览" {
		t.Fatalf("follow system Chinese -> %q", got)
	}
	a.prefs.Language = "en"
	a.prefs.Language = "简体中文"
	if got := a.tr("Browser"); got != "浏览" {
		t.Fatalf("Browser -> %q", got)
	}
	if got := a.tr("Command palette"); got != "命令面板" {
		t.Fatalf("palette -> %q", got)
	}
	if got := a.tr("not in the dictionary"); got != "not in the dictionary" {
		t.Fatalf("fallback %q", got)
	}
	testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"浏览", "状态", "命令行", "慢日志", "监视器", "发布/订阅", "命令日志"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestShortcutsOpenPaletteAndPreferences(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if !a.paletteOpen {
		t.Fatal("Cmd+K did not open the palette")
	}
	if !tt.HasText("Command palette") {
		t.Fatalf("palette texts %q", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if a.paletteOpen {
		t.Fatal("Cmd+K did not close the palette")
	}
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if !a.prefsOpen || a.paletteOpen {
		t.Fatalf("prefs %v palette %v", a.prefsOpen, a.paletteOpen)
	}
	st := ui.NewTester(a.settingsView, 820, 620)
	for _, want := range []string{"General", "Default format", "Version", "0.1.0"} {
		if !st.HasText(want) {
			t.Fatalf("missing %q in %q", want, st.Texts())
		}
	}
	if err := st.Click("Appearance"); err != nil {
		t.Fatal(err)
	}
	st.Frame()
	for _, want := range []string{"Appearance", "Language", "Translucent", "Background", "Follow System", "Light", "Dark"} {
		if !st.HasText(want) {
			t.Fatalf("appearance missing %q in %q", want, st.Texts())
		}
	}
}

func TestShortcutToolPages(t *testing.T) {
	a := newApp()
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Key(ui.Cmd, ui.Key2)
	tt.Frame()
	if s.tool != "status" {
		t.Fatalf("tool %q", s.tool)
	}
	tt.Key(ui.Cmd, ui.Key7)
	tt.Frame()
	if s.tool != "cmdlog" {
		t.Fatalf("tool %q", s.tool)
	}
}

func TestPaletteFilters(t *testing.T) {
	a := newApp()
	a.paletteOpen = true
	a.paletteQuery = "preferences"
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("Preferences") || tt.HasText("Use light theme") {
		t.Fatalf("palette filter %q", tt.Texts())
	}
}

func TestLanguageNormalization(t *testing.T) {
	p := Preferences{Language: "中文"}
	p.normalize()
	if p.Language != "zh-Hans" {
		t.Fatalf("legacy language %q", p.Language)
	}
	p = Preferences{}
	p.normalize()
	if p.Language != "system" {
		t.Fatalf("default language %q", p.Language)
	}
}

func TestRememberActiveSession(t *testing.T) {
	a := newApp()
	a.prefsPath = filepath.Join(t.TempDir(), "preferences.json")
	s := testSession(a, ConnConfig{Addr: "127.0.0.1:6379"})
	s.db = 3
	a.rememberActiveSession()
	got, err := loadPreferences(a.prefsPath)
	if err != nil || got.LastProfile != "test" || got.LastDB != 3 {
		t.Fatalf("last session %+v %v", got, err)
	}
}

func TestStartupPreferenceUI(t *testing.T) {
	a := newApp()
	a.prefsOpen = true
	st := ui.NewTester(a.settingsView, 820, 620)
	if !st.HasText("Open on launch") || !st.HasText("Connection list") {
		t.Fatalf("startup setting: %q", st.Texts())
	}
	if err := st.Click("Connection list"); err != nil {
		t.Fatal(err)
	}
	st.Frame()
	if err := st.Click("Last session"); err != nil {
		t.Fatal(err)
	}
	st.Frame()
	if a.prefs.Startup != "Last session" {
		t.Fatalf("startup %q", a.prefs.Startup)
	}
}

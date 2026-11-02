package app

// User preferences, saved next to profiles.json.

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CustomDecoder struct {
	Name          string `json:"name"`
	Command       string `json:"command"`
	EncodeCommand string `json:"encodeCommand,omitempty"`
}

type Preferences struct {
	ScanSize               float64  `json:"scanSize"`
	ValuePageSize          float64  `json:"valuePageSize"`
	RefreshSeconds         float64  `json:"refreshSeconds"`
	EditorFont             string   `json:"editorFont"`
	EditorSize             float64  `json:"editorSize"`
	CLIFont                string   `json:"cliFont"`
	CLISize                float64  `json:"cliSize"`
	LineNumbers            *bool    `json:"lineNumbers,omitempty"`
	Folding                *bool    `json:"folding,omitempty"`
	SoftWrap               bool     `json:"softWrap,omitempty"`
	Groups                 []string `json:"groups,omitempty"`
	Theme                  string   `json:"theme"`      // System, Light, Dark
	Accent                 string   `json:"accent"`     // Blue, Violet, Green, Orange, Red, Pink
	Background             string   `json:"background"` // washTable key
	Vibrancy               *bool    `json:"vibrancy,omitempty"`
	BackgroundTransparency float64  `json:"backgroundTransparency"`
	Language               string   `json:"language"` // BCP 47 tag, or system
	DefaultFormat          string   `json:"defaultFormat"`
	DefaultDecode          string   `json:"defaultDecode"`
	Startup                string   `json:"startup"` // Connections, Last session
	LastProfile            string   `json:"lastProfile,omitempty"`
	LastDB                 int      `json:"lastDB,omitempty"`
	SidebarWidth           float32  `json:"sidebarWidth,omitempty"`
	SidebarHidden          bool     `json:"sidebarHidden,omitempty"`
	// CustomDecoder is the old single-decoder field; normalize migrates it.
	CustomDecoder  string          `json:"customDecoder,omitempty"`
	CustomDecoders []CustomDecoder `json:"customDecoders,omitempty"`
	CheckUpdates   bool            `json:"checkUpdates"`
}

func defaultPreferences() Preferences {
	return Preferences{
		ScanSize: 1000, ValuePageSize: 200, RefreshSeconds: 5,
		EditorFont: "monospace", EditorSize: 13, CLIFont: "monospace", CLISize: 12,
		LineNumbers: boolPtr(true), Folding: boolPtr(true),
		Theme: "System", Accent: "Blue", Background: "Petal",
		Vibrancy: boolPtr(true), BackgroundTransparency: 40, Language: "system",
		DefaultFormat: "Raw", DefaultDecode: "None",
		Startup:      "Connection list",
		SidebarWidth: sidebarW,
		CheckUpdates: true,
	}
}

func (p *Preferences) normalize() {
	if p.SidebarWidth < 240 || p.SidebarWidth > 360 {
		p.SidebarWidth = sidebarW
	}
	if p.ScanSize < 1 || p.ScanSize > 100000 {
		p.ScanSize = 1000
	}
	if p.ValuePageSize < 1 || p.ValuePageSize > 10000 {
		p.ValuePageSize = 200
	}
	if p.RefreshSeconds < 1 || p.RefreshSeconds > 3600 {
		p.RefreshSeconds = 5
	}
	if p.EditorSize < 8 || p.EditorSize > 40 {
		p.EditorSize = 13
	}
	if p.CLISize < 8 || p.CLISize > 40 {
		p.CLISize = 12
	}
	if strings.TrimSpace(p.EditorFont) == "" {
		p.EditorFont = "monospace"
	}
	if strings.TrimSpace(p.CLIFont) == "" {
		p.CLIFont = "monospace"
	}
	if p.LineNumbers == nil {
		p.LineNumbers = boolPtr(true)
	}
	if p.Folding == nil {
		p.Folding = boolPtr(true)
	}
	switch p.Theme {
	case "Light", "Dark":
	default:
		p.Theme = "System"
	}
	p.Language = normalizeLanguage(p.Language)

	switch p.Accent {
	case "Violet", "Green", "Orange", "Red", "Pink", "Blue":
	default:
		p.Accent = "Blue"
	}
	if _, ok := washTable[p.Background]; !ok {
		p.Background = "Petal"
	}
	if p.Vibrancy == nil {
		p.Vibrancy = boolPtr(true)
	}
	if math.IsNaN(p.BackgroundTransparency) || p.BackgroundTransparency < 0 || p.BackgroundTransparency > 100 {
		p.BackgroundTransparency = 40
	}
	switch p.Startup {
	case "Last session", "Connection list":
	case "Connections":
		p.Startup = "Connection list"
	default:
		p.Startup = "Connection list"
	}
	if !containsString(valueFormats, p.DefaultFormat) {
		p.DefaultFormat = "Raw"
	}
	legacy := strings.TrimSpace(p.CustomDecoder)
	if legacy != "" {
		if len(p.CustomDecoders) == 0 {
			p.CustomDecoders = append(p.CustomDecoders, CustomDecoder{Name: "Custom", Command: legacy})
		}
		p.CustomDecoder = ""
	}
	seen := map[string]bool{}
	out := p.CustomDecoders[:0]
	for _, d := range p.CustomDecoders {
		d.Name = strings.TrimSpace(d.Name)
		d.Command = strings.TrimSpace(d.Command)
		d.EncodeCommand = strings.TrimSpace(d.EncodeCommand)
		if d.Name == "" || d.Command == "" || seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		out = append(out, d)
	}
	p.CustomDecoders = out
	if !containsString(valueDecodes, p.DefaultDecode) && !p.hasCustomDecoder(p.DefaultDecode) {
		if p.DefaultDecode == "Custom" {
			if len(p.CustomDecoders) > 0 {
				p.DefaultDecode = "Custom: " + p.CustomDecoders[0].Name
			} else {
				p.DefaultDecode = "None"
			}
		} else {
			p.DefaultDecode = "None"
		}
	}
}

func boolPtr(v bool) *bool { return &v }

func (p Preferences) translucent() bool { return p.Vibrancy == nil || *p.Vibrancy }

func (p Preferences) hasCustomDecoder(option string) bool {
	name, ok := strings.CutPrefix(option, "Custom: ")
	if !ok {
		return false
	}
	for _, d := range p.CustomDecoders {
		if d.Name == name {
			return true
		}
	}
	return false
}

func (p Preferences) decoderCommand(name string) (string, bool) {
	for _, d := range p.CustomDecoders {
		if d.Name == name {
			return d.Command, true
		}
	}
	return "", false
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func loadPreferences(path string) (Preferences, error) {
	p := defaultPreferences()
	data, err := readBoundedFile(path, maxProfileBytes)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return defaultPreferences(), err
	}
	p.normalize()
	return p, nil
}

func savePreferences(path string, p Preferences) (err error) {
	p.normalize()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".preferences-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// decodeChoices lists the built-in decoders and the user's named ones.
func (a *App) decodeChoices() []string {
	out := append([]string(nil), valueDecodes...)
	for _, d := range a.prefs.CustomDecoders {
		out = append(out, "Custom: "+d.Name)
	}
	return out
}

func (a *App) decoderCommand(name string) (string, bool) {
	return a.prefs.decoderCommand(name)
}

func (a *App) decoderByName(name string) (CustomDecoder, bool) {
	for _, d := range a.prefs.CustomDecoders {
		if d.Name == name {
			return d, true
		}
	}
	return CustomDecoder{}, false
}

func (p Preferences) refreshInterval() time.Duration {
	if p.RefreshSeconds < 1 {
		return 5 * time.Second
	}
	return time.Duration(p.RefreshSeconds * float64(time.Second))
}

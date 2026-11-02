package app

// Portions of the desktop layout and controls are adapted from kero.
// Copyright (c) 2026 EGOIST. MIT License; see third_party/licenses/kero.txt.

// The window's design language follows kero's (MIT): a semantic palette
// for each appearance, a borderless theme, a wash under soft cards, and a
// few levels of elevation. The values are adapted for a data browser.

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

type palette struct {
	dark bool

	card  ui.Color // the working surface
	text  ui.Color
	muted ui.Color
	faint ui.Color

	accent   ui.Color
	onAccent ui.Color
	danger   ui.Color
	warning  ui.Color
	success  ui.Color

	selection    ui.Color
	rowHover     ui.Color
	rowSelected  ui.Color
	control      ui.Color
	controlDown  ui.Color
	chip         ui.Color
	divider      ui.Color
	cardBorder   ui.Color
	shadow       ui.Color
	shadowStrong ui.Color
}

var lightPalette = palette{
	card:  ui.Hex("#ffffff"),
	text:  ui.Hex("#1d1d1f"),
	muted: ui.Hex("#6e6e78"),
	faint: ui.Hex("#747480"),

	accent:   ui.Hex("#3b6cf6"),
	onAccent: ui.Hex("#ffffff"),
	danger:   ui.Hex("#d93a35"),
	warning:  ui.Hex("#c27b0f"),
	success:  ui.Hex("#1f8f4e"),

	selection:    ui.RGBA(59, 108, 246, 0.22),
	rowHover:     ui.RGBA(0, 0, 0, 0.045),
	rowSelected:  ui.RGBA(255, 255, 255, 0.9),
	control:      ui.Hex("#ffffff"),
	controlDown:  ui.Hex("#efeff2"),
	chip:         ui.RGBA(0, 0, 0, 0.045),
	divider:      ui.RGBA(0, 0, 0, 0.07),
	cardBorder:   ui.RGBA(0, 0, 0, 0.06),
	shadow:       ui.RGBA(20, 20, 50, 0.10),
	shadowStrong: ui.RGBA(20, 20, 50, 0.18),
}

var darkPalette = palette{
	dark:  true,
	card:  ui.Hex("#232328"),
	text:  ui.Hex("#ececf0"),
	muted: ui.Hex("#a9a9b4"),
	faint: ui.Hex("#a0a0ad"),

	accent:   ui.Hex("#6b93ff"),
	onAccent: ui.Hex("#ffffff"),
	danger:   ui.Hex("#ff6961"),
	warning:  ui.Hex("#f0a33a"),
	success:  ui.Hex("#4fd17a"),

	selection:    ui.RGBA(107, 147, 255, 0.28),
	rowHover:     ui.RGBA(255, 255, 255, 0.055),
	rowSelected:  ui.RGBA(255, 255, 255, 0.1),
	control:      ui.Hex("#404047"),
	controlDown:  ui.Hex("#55555e"),
	chip:         ui.RGBA(255, 255, 255, 0.07),
	divider:      ui.RGBA(255, 255, 255, 0.08),
	cardBorder:   ui.RGBA(255, 255, 255, 0.08),
	shadow:       ui.RGBA(0, 0, 0, 0.35),
	shadowStrong: ui.RGBA(0, 0, 0, 0.48),
}

// accentColors are the appearance presets, with a brighter dark variant.
var accentColors = map[string]struct{ light, dark ui.Color }{
	"Blue":   {ui.Hex("#3b6cf6"), ui.Hex("#6b93ff")},
	"Violet": {ui.Hex("#7c5cf6"), ui.Hex("#a78bfa")},
	"Green":  {ui.Hex("#0f9f6e"), ui.Hex("#34d399")},
	"Orange": {ui.Hex("#d97706"), ui.Hex("#fbbf24")},
	"Red":    {ui.Hex("#dc3a4a"), ui.Hex("#f87171")},
	"Pink":   {ui.Hex("#db2777"), ui.Hex("#f472b6")},
}

func accentColor(name string, dark bool) ui.Color {
	if a, ok := accentColors[name]; ok {
		if dark {
			return a.dark
		}
		return a.light
	}
	if dark {
		return darkPalette.accent
	}
	return lightPalette.accent
}

type washColors struct {
	light, dark []ui.Color
}

var washTable = map[string]washColors{
	"Petal": {
		light: []ui.Color{ui.Hex("#ebe0f3"), ui.Hex("#f3e7f1"), ui.Hex("#f6efcf")},
		dark:  []ui.Color{ui.Hex("#17141f"), ui.Hex("#191620"), ui.Hex("#1d1a16")},
	},
	"Mist": {
		light: []ui.Color{ui.Hex("#e3e9f4"), ui.Hex("#ebeff6"), ui.Hex("#f2f1ec")},
		dark:  []ui.Color{ui.Hex("#12151b"), ui.Hex("#14171d"), ui.Hex("#17181b")},
	},
	"Meadow": {
		light: []ui.Color{ui.Hex("#e2eee5"), ui.Hex("#ebf2e7"), ui.Hex("#f5f0d6")},
		dark:  []ui.Color{ui.Hex("#111814"), ui.Hex("#141a16"), ui.Hex("#181a14")},
	},
	"Dusk": {
		light: []ui.Color{ui.Hex("#e4dbf3"), ui.Hex("#efdeee"), ui.Hex("#f7e6cf")},
		dark:  []ui.Color{ui.Hex("#191321"), ui.Hex("#1e161e"), ui.Hex("#221a17")},
	},
	// Additional background colors from kero v2.1.31, theme.go.
	"Tide": {
		light: []ui.Color{ui.Hex("#dbebef"), ui.Hex("#e3eff1"), ui.Hex("#ecf3f0"), ui.Hex("#f2f2e8")},
		dark:  []ui.Color{ui.Hex("#001b22"), ui.Hex("#001e1f"), ui.Hex("#0b1e1b")},
	},
	"Apricot": {
		light: []ui.Color{ui.Hex("#f5e1d6"), ui.Hex("#f8e7dc"), ui.Hex("#f8eedf"), ui.Hex("#f6f0da")},
		dark:  []ui.Color{ui.Hex("#2a1207"), ui.Hex("#291507"), ui.Hex("#25190a")},
	},
	"Aurora": {
		light: []ui.Color{ui.Hex("#dbefe8"), ui.Hex("#e1ebf2"), ui.Hex("#e8e4f4"), ui.Hex("#f0e6f1")},
		dark:  []ui.Color{ui.Hex("#001f13"), ui.Hex("#06192b"), ui.Hex("#1e122a")},
	},
	"Dune": {
		light: []ui.Color{ui.Hex("#ede4d8"), ui.Hex("#f1e9de"), ui.Hex("#f4eee4"), ui.Hex("#f3efe8")},
		dark:  []ui.Color{ui.Hex("#22190b"), ui.Hex("#211a0e"), ui.Hex("#201b12")},
	},
	"Paper": {
		light: []ui.Color{ui.Hex("#f6f6f7"), ui.Hex("#f7f7f8"), ui.Hex("#f8f8f8")},
		dark:  []ui.Color{ui.Hex("#141416"), ui.Hex("#151517"), ui.Hex("#161618")},
	},
	"Plain": {
		light: []ui.Color{ui.Hex("#eceef4"), ui.Hex("#eceef4"), ui.Hex("#eceef4")},
		dark:  []ui.Color{ui.Hex("#141418"), ui.Hex("#141418"), ui.Hex("#141418")},
	},
}

func washGradient(name string, dark bool) ui.LinearGradient {
	w, ok := washTable[name]
	if !ok {
		w = washTable["Petal"]
	}
	colors := w.light
	if dark {
		colors = w.dark
	}
	stops := make([]ui.GradientStop, len(colors))
	for i, color := range colors {
		stops[i] = ui.GradientStop{Color: color, At: float32(i) / float32(len(colors)-1)}
	}
	return ui.LinearGradient{Angle: 180, Oklab: true, Stops: stops}
}

// borderless returns the theme kivrix draws with: kero's soft fills
// instead of outlines, the palette's accent, and the same spacing rhythm.
func borderless(c *ui.Context, pal *palette) *ui.Theme {
	t := *c.Theme()
	t.Dark = pal.dark
	t.Background = pal.card
	t.Text = pal.text
	t.TextMuted = pal.muted
	t.Accent = pal.accent
	t.AccentText = pal.onAccent
	t.AccentHover = pal.accent.Mix(ui.Hex("#ffffff"), 0.08)
	t.AccentPressed = pal.accent.Mix(ui.Hex("#000000"), 0.1)
	t.Danger = pal.danger
	t.Warning = pal.warning
	t.Success = pal.success
	t.Selection = pal.selection
	t.Focus = pal.accent.Alpha(0.6)
	t.Border = pal.divider
	t.Surface = pal.control
	t.SurfaceHover = pal.card
	t.SurfacePressed = pal.controlDown
	t.Inverse = pal.text
	t.InverseText = pal.card
	t.Scrollbar = pal.faint.Alpha(0.55)
	t.Radius = 8
	t.Spacing = 4
	t.FontSize = 13
	return &t
}

// backdrop lets the window's blurred material show through the wash.
// Keep it opaque when the platform has no material, or translucency is off.
func (a *App) backdrop(c *ui.Context, pal *palette) {
	alpha := float32(1)
	if c.Vibrancy() && a.prefs.translucent() {
		// Keep a faint tint at 100%, with a smooth change across the full slider.
		const minTintOpacity = 0.25
		alpha = 1 - (1-minTintOpacity)*float32(a.prefs.BackgroundTransparency/100)
	}
	gradient := washGradient(a.prefs.Background, pal.dark)
	for i := range gradient.Stops {
		gradient.Stops[i].Color = gradient.Stops[i].Color.Alpha(alpha)
	}
	c.Root().LinearGradient(gradient)
}

// raised lifts a card a little off the wash.
func raised(e ui.Element, pal *palette) ui.Element {
	if pal.dark {
		return e.Border(1, pal.cardBorder).
			Shadow(0, 1, 2, 0, pal.shadow).
			Shadow(0, 8, 24, -8, pal.shadowStrong)
	}
	return e.Border(1, pal.cardBorder).
		Shadow(0, 1, 2, 0, pal.shadow).
		Shadow(0, 10, 30, -10, pal.shadowStrong)
}

// chosen marks the selected row of the sidebar, lighter than the sidebar
// in the light and raised in the dark.
func chosen(e ui.Element, pal *palette) ui.Element {
	if pal.dark {
		return raised(e, pal)
	}
	return e.Background(pal.card).
		Shadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.06)).
		Shadow(0, 0.5, 2, 0, ui.RGBA(0, 0, 0, 0.05))
}

// typeColor is the color of a Redis type, as tiny-rdm marks its tags.
func typeColor(typ string, pal *palette) ui.Color {
	switch strings.ToLower(typ) {
	case "string":
		return ui.Hex("#8b5cf6")
	case "hash":
		return ui.Hex("#3b82f6")
	case "list":
		return ui.Hex("#10b981")
	case "set":
		return ui.Hex("#f59e0b")
	case "zset":
		return ui.Hex("#ef4444")
	case "stream":
		return ui.Hex("#ec4899")
	case "json", "rejson":
		return ui.Hex("#828766")
	case "none":
		return pal.faint
	default:
		return pal.muted
	}
}

func typeTag(c *ui.Context, typ string, pal *palette) ui.Element {
	color := typeColor(typ, pal)
	return ui.Row(c).Height(20).Padding(0, 7).Gap(5).Radius(6).
		Background(color.Alpha(0.13)).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(5, 5).Radius(2.5).Background(color)
		ui.Text(c, strings.ToUpper(typ)).FontSize(10).FontWeight(600).TextColor(color)
	})
}

func statusDot(c *ui.Context, color ui.Color) ui.Element {
	return ui.Box(c).Size(5, 5).Radius(2.5).Background(color)
}

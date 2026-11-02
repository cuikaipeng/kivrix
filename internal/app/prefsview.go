package app

// Portions of the desktop layout and controls are adapted from kero.
// Copyright (c) 2026 EGOIST. MIT License; see third_party/licenses/kero.txt.

// Preferences, keyboard shortcuts and the command palette.

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func (a *App) openPreferences() {
	a.paletteOpen = false
	a.prefsOpen = true
	a.decoderEdit = false
	a.decoderErr = ""
	// Tests render settingsView without a native window.
	if a.win == nil {
		return
	}
	if a.settingsWin != nil {
		a.settingsWin.Show()
		a.settingsWin.Focus()
		return
	}
	opts := mygo.WindowOptions{
		Title: a.tr("Settings"), Width: 820, Height: 620, MinWidth: 700, MinHeight: 480,
		StateKey: "settings", Vibrancy: a.windowVibrancy(),
		BackgroundColor: "light-dark(#f3eef6, #17141f)",
		Content:         ui.View(a.settingsView),
	}
	if runtime.GOOS == "darwin" {
		opts = framed(opts)
	}
	a.settingsWin = mygo.NewWindow(opts)
	a.settingsWin.OnClosed(func() {
		a.settingsWin = nil
		a.prefsOpen = false
	})
}

func (a *App) openPalette() {
	if a.settingsWin != nil {
		a.settingsWin.Close()
	}
	a.prefsOpen = false
	a.paletteOpen = true
	a.paletteQuery = ""
	a.paletteIndex = 0
	a.paletteReveal = true
}

func (a *App) togglePalette() {
	if a.paletteOpen {
		a.paletteOpen = false
		return
	}
	a.openPalette()
}

func (a *App) applyLocale() {
	a.prefs.Language = normalizeLanguage(a.prefs.Language)
	tag := a.prefs.Language
	if tag == "system" {
		tag = ""
	}
	mygo.App.SetLocale(tag)
	mygo.App.SetMenu(a.applicationMenu())
}

func (a *App) savePrefs() {
	for _, s := range a.sessions {
		s.autoEvery = a.prefs.refreshInterval()
	}
	// Appearance and language changes also affect the main window, which
	// is a separate window from Settings. Repaint it now instead of when
	// Settings closes.
	if a.win != nil {
		a.applyTheme()
		a.applyLocale()
		a.win.Invalidate()
	}
	if a.settingsWin != nil {
		a.settingsWin.Invalidate()
	}
	if a.prefsPath == "" {
		return
	}
	if err := savePreferences(a.prefsPath, a.prefs); err != nil {
		a.setStatusErr(err)
	}
}

const (
	settingsGeneral = iota
	settingsAppearance
	settingsDecoders
	settingsShortcuts
)

var settingsPages = []struct {
	tab   int
	label string
	icon  *ui.SVG
}{
	{settingsGeneral, "General", iconSettings},
	{settingsAppearance, "Appearance", iconPalette},
	{settingsDecoders, "Decoders", iconBraces},
	{settingsShortcuts, "Shortcuts", iconKeyboard},
}

// settingsView is the Settings window's content: a native window with the
// kero layout, a navigation rail on the app wash and a white page card.
func (a *App) settingsView(c *ui.Context) {
	if !a.prefsOpen {
		return
	}
	pal := a.paletteFor(c)
	c.SetTheme(borderless(c, pal))
	a.backdrop(c, pal)
	// On macOS the hidden title bar leaves the traffic lights over the
	// wash, as in kero; other platforms keep their native title bar.
	top := float32(8)
	if runtime.GOOS == "darwin" {
		top = topBarH + 8
	}
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(212).Shrink(0).Padding(top, 14, 12, 10).Gap(3).Children(func() {
			for _, page := range settingsPages {
				a.settingsNav(c, pal, page.tab, a.tr(page.label), page.icon)
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Padding(8).Children(func() {
			card := raised(ui.Column(c).Grow(1).MinHeight(0).Radius(14).
				Background(pal.card).Clip(), pal)
			card.Children(func() {
				title := a.tr("General")
				for _, page := range settingsPages {
					if page.tab == a.prefsTab {
						title = a.tr(page.label)
					}
				}
				ui.Row(c).Height(54).Shrink(0).Padding(0, 20).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, title).FontSize(17).FontWeight(650).TextColor(pal.text)
				})
				ui.Box(c).Height(1).Shrink(0).Background(pal.divider)
				ui.Scroll(c).Grow(1).MinHeight(0).Padding(16, 20, 20, 16).Children(func() {
					ui.Column(c).Gap(18).Children(func() {
						switch a.prefsTab {
						case settingsAppearance:
							a.viewAppearancePreferences(c, pal)
						case settingsDecoders:
							a.viewDecoderPreferences(c, pal)
						case settingsShortcuts:
							a.viewShortcutPreferences(c, pal)
						default:
							a.viewGeneralPreferences(c, pal)
						}
					})
				})
			})
		})
	})
}

func (a *App) settingsNav(c *ui.Context, pal *palette, tab int, label string, icon *ui.SVG) {
	selected := a.prefsTab == tab
	row := ui.Row(c.Key(fmt.Sprintf("pref-nav-%d", tab))).Height(32).Padding(0, 9).Gap(9).
		Radius(8).AlignItems(ui.Center).FocusRing(false)
	if selected {
		chosen(row, pal)
	}
	row.Children(func() {
		ui.Icon(c, icon).Size(14, 14).TextColor(pal.muted)
		ui.Text(c, label).FontSize(13).TextColor(pal.text).SingleLine()
	})
	if row.Clicked() && !selected {
		a.prefsTab = tab
		a.decoderEdit = false
	}
}

var prefGroupRows int

func (a *App) prefGroup(c *ui.Context, pal *palette, title string, fn func()) {
	ui.Column(c).Gap(7).Children(func() {
		if title != "" {
			ui.Text(c, a.tr(title)).FontSize(11).FontWeight(600).TextColor(pal.muted).Padding(0, 4)
		}
		prefGroupRows = 0
		ui.Column(c).Radius(12).Background(pal.chip).Padding(2, 14).Children(fn)
	})
}

func (a *App) prefRow(c *ui.Context, pal *palette, label, detail string, control func()) {
	if prefGroupRows > 0 {
		ui.Box(c).Height(1).Shrink(0).Background(pal.text.Alpha(0.07))
	}
	prefGroupRows++
	ui.Row(c.Key("pref-row-"+label)).Padding(10, 0).Gap(14).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, a.tr(label)).FontSize(12.5).TextColor(pal.text)
			if detail != "" {
				ui.Text(c, a.tr(detail)).FontSize(10.5).TextColor(pal.muted).LineHeight(1.3)
			}
		})
		ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(control)
	})
}

func (a *App) prefSwitch(c *ui.Context, pal *palette, label string, on *bool, changed func()) {
	if prefGroupRows > 0 {
		ui.Box(c).Height(1).Shrink(0).Background(pal.text.Alpha(0.07))
	}
	prefGroupRows++
	sw := ui.SwitchBase(c.Key("switch-"+label), on).Padding(10, 0).Gap(14).
		AlignItems(ui.Center).Label(a.tr(label)).FocusRing(false)
	sw.Children(func() {
		ui.Text(c, a.tr(label)).FontSize(12.5).TextColor(pal.text).Grow(1).MinWidth(0)
		color := pal.text.Alpha(0.14)
		if pal.dark {
			color = ui.RGBA(255, 255, 255, 0.2)
		}
		if *on {
			color = pal.accent
		}
		track := ui.Box(c).Size(38, 22).Radius(11).Padding(2).Shrink(0).Background(color)
		if sw.FocusVisible() {
			track.DrawOver(func(p *ui.Painter, r ui.Rect) {
				p.FocusRing(r, [4]float32{11, 11, 11, 11})
			})
		}
		track.Children(func() {
			x := float32(0)
			if *on {
				x = 16
			}
			ui.Box(c).Size(18, 18).Radius(9).Background(ui.Hex("#ffffff")).Translate(x, 0).
				Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.22)).Shadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.06))
		})
	})
	sw.OnChange(changed)
}

func (a *App) prefSegmented(c *ui.Context, pal *palette, key string, value *string, ids, labels []string) {
	i := 0
	for n := range ids {
		if ids[n] == *value {
			i = n
			break
		}
	}
	track, thumb := ui.RGBA(0, 0, 0, 0.06), ui.Hex("#ffffff")
	if pal.dark {
		track, thumb = ui.RGBA(255, 255, 255, 0.07), ui.RGBA(255, 255, 255, 0.2)
	}
	seg := ui.SegmentedBase(c.Key(key), &i, len(labels))
	seg.Track.Height(24).Padding(2).Radius(7).Background(track).AlignItems(ui.Center).Children(func() {
		for n, label := range labels {
			if n > 0 {
				sep := ui.Box(c).Width(1).Height(12).Shrink(0)
				if n != i && n-1 != i {
					sep.Background(pal.text.Alpha(0.14))
				}
			}
			part := seg.Segment(n).FillHeight().Padding(0, 12).Radius(5).Center()
			if n == i {
				part.Background(thumb)
				if pal.dark {
					part.Shadow(0, 1, 1, 0, ui.RGBA(0, 0, 0, 0.25))
				} else {
					part.Shadow(0, 0.5, 1.5, 0, ui.RGBA(0, 0, 0, 0.16)).
						Shadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.04))
				}
			}
			part.Children(func() {
				ui.Text(c, a.tr(label)).FontSize(12).SingleLine().TextColor(pal.text)
			})
		}
	})
	seg.Track.OnChange(func() {
		*value = ids[i]
		a.savePrefs()
	})
}

func (a *App) prefPopup(c *ui.Context, pal *palette, key string, value *string, options []string, width float32, labels ...func(string) string) {
	label := func(value string) string { return a.tr(value) }
	if len(labels) > 0 {
		label = labels[0]
	}
	before := *value
	sel := ui.SelectBase(c.Key(key), value)
	trig := sel.Trigger.MaxWidth(width)
	open := sel.Open()
	tint := pal.text.Alpha(0.065)
	if pal.dark {
		tint = ui.RGBA(255, 255, 255, 0.09)
	}
	trig.Height(28).Padding(0, 3, 0, 10).Gap(6).Radius(14).AlignItems(ui.Center)
	bg, disc := ui.Transparent, tint
	switch {
	case open:
		bg, disc = tint, ui.Transparent
	case trig.Hovered():
		bg, disc = tint, ui.Transparent
	}
	trig.Background(bg)
	trig.Children(func() {
		ui.Text(c, label(*value)).FontSize(12.5).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		ui.Box(c).Size(22, 22).Radius(11).Center().Shrink(0).Background(disc).Children(func() {
			ui.Icon(c, iconUpDown).Size(14, 14).TextColor(pal.text.Alpha(0.85))
		})
	})
	sel.Popup(func(panel ui.Element) {
		panel.Padding(5).Radius(12).Background(pal.card).Border(1, pal.cardBorder).
			Shadow(0, 8, 24, -8, pal.shadowStrong)
		ui.Scroll(c).MaxHeight(280).Children(func() {
			ui.Column(c).Children(func() {
				for _, opt := range options {
					item := sel.Item(opt).Height(26).Padding(0, 8).Gap(8).Radius(6).AlignItems(ui.Center)
					if opt == *value {
						item.Background(pal.rowSelected)
					} else if item.Hovered() {
						item.Background(pal.rowHover)
					}
					item.Children(func() {
						if opt == *value {
							ui.Icon(c, iconCheck).Size(12, 12).TextColor(pal.accent)
						} else {
							ui.Box(c).Size(12, 12)
						}
						ui.Text(c, label(opt)).FontSize(12).TextColor(pal.text).SingleLine()
					})
				}
			})
		})
	})
	if *value != before {
		a.savePrefs()
	}
}

func (a *App) viewGeneralPreferences(c *ui.Context, pal *palette) {
	a.prefGroup(c, pal, "Updates", func() {
		a.prefSwitch(c, pal, "Check for updates on launch", &a.prefs.CheckUpdates, a.savePrefs)
		a.prefRow(c, pal, "Version", appVersion, func() {
			label := a.tr("Check for updates")
			if a.updateChecking {
				label = a.tr("Checking…")
			}
			if textButton(c, "prefs-update", label, false, false, pal).Disabled(a.updateChecking || a.updateDownloading || a.updateReady).Clicked() {
				a.checkUpdates()
			}
			if label, action := a.updateAction(); label != "" {
				textButton(c, "prefs-download", label, true, false, pal).Disabled(action == nil).OnClick(action)
			}
		})
	})
	if a.updateStatus.key != "" {
		color := pal.muted
		if a.updateErr != "" {
			color = pal.danger
		}
		ui.Text(c, a.updateStatus.text(a)).FontSize(11).TextColor(color).LineHeight(1.4)
	}
	a.prefGroup(c, pal, "Startup", func() {
		a.prefRow(c, pal, "Open on launch", "", func() {
			a.prefPopup(c, pal, "startup", &a.prefs.Startup, []string{"Connection list", "Last session"}, 180)
		})
	})
	a.prefGroup(c, pal, "Defaults", func() {
		a.prefRow(c, pal, "Default format", "", func() {
			a.prefPopup(c, pal, "default-format", &a.prefs.DefaultFormat, valueFormats, 165)
		})
		a.prefRow(c, pal, "Default decode", "", func() {
			a.prefPopup(c, pal, "default-decode", &a.prefs.DefaultDecode, a.decodeChoices(), 180)
		})
	})
	a.viewBehaviorPreferences(c, pal)
}

func (a *App) viewAppearancePreferences(c *ui.Context, pal *palette) {
	a.prefGroup(c, pal, "", func() {
		a.prefRow(c, pal, "Appearance", "", func() {
			a.prefSegmented(c, pal, "theme", &a.prefs.Theme,
				[]string{"System", "Light", "Dark"},
				[]string{"Follow System", "Light", "Dark"})
		})
		a.prefRow(c, pal, "Language", "", func() {
			a.prefPopup(c, pal, "language", &a.prefs.Language, languageOptions(), 170, a.languageLabel)
		})
		a.prefSwitch(c, pal, "Translucent window", a.prefs.Vibrancy, func() {
			a.savePrefs()
			a.applyVibrancy()
		})
		a.prefRow(c, pal, "Background transparency", "A subtle tint remains at 100%.", func() {
			ui.Slider(c.Key("background-transparency"), &a.prefs.BackgroundTransparency, 0, 100).
				Width(140).Step(1).Label(a.tr("Background transparency")).Disabled(!a.prefs.translucent()).
				OnChange(func() {
					if a.win != nil {
						a.win.Invalidate()
					}
					a.schedulePrefsSave()
				})
			ui.Text(c, fmt.Sprintf("%.0f%%", a.prefs.BackgroundTransparency)).
				Width(36).FontSize(12.5).TextAlign(ui.End).TextColor(pal.muted)
		})
	})
	ui.Column(c).Gap(7).Children(func() {
		ui.Text(c, a.tr("Background")).FontSize(11).FontWeight(600).TextColor(pal.muted).Padding(0, 4)
		ui.Row(c).Gap(12).Wrap().Children(func() {
			for _, bg := range []string{
				"Petal", "Mist", "Meadow", "Dusk", "Tide", "Apricot", "Aurora", "Dune", "Paper", "Plain",
			} {
				a.appearanceBackgroundTile(c, pal, bg, a.tr(bg))
			}
		})
	})
}

func (a *App) appearanceBackgroundTile(c *ui.Context, pal *palette, id, label string) {
	selected := a.prefs.Background == id
	tile := ui.ButtonBase(c.Key("bg-" + id)).Column().Width(104).Gap(6).AlignItems(ui.Stretch).Label(label)
	tile.Children(func() {
		sw := raised(ui.Box(c).Size(104, 68).Radius(12), pal).Clip()
		sw.LinearGradient(washGradient(id, pal.dark))
		sw.Children(func() {
			ui.Box(c).Absolute().Top(13).Left(28).Right(8).Bottom(-6).Radius(7).Background(pal.card)
			ui.Box(c).Absolute().Top(21).Left(10).Width(12).Height(4).Radius(2).
				Background(pal.muted.Alpha(0.5))
			if selected {
				ui.Box(c).Absolute().Top(6).Right(6).Size(18, 18).Radius(9).Center().
					Background(pal.accent).Children(func() {
					ui.Box(c).Size(10, 10).Draw(func(p *ui.Painter, r ui.Rect) {
						ck := (&ui.Path{}).MoveTo(r.X+1, r.Y+5.2).LineTo(r.X+4, r.Y+8).LineTo(r.X+9, r.Y+2)
						p.StrokePath(ck, 1.6, pal.onAccent)
					})
				})
			}
		})
		lbl := ui.Text(c, label).FontSize(11.5).TextColor(pal.muted).TextAlign(ui.Center)
		if selected {
			lbl.TextColor(pal.text).FontWeight(600)
		}
	})
	if tile.Clicked() {
		a.prefs.Background = id
		a.savePrefs()
	}
}

func (a *App) viewShortcutPreferences(c *ui.Context, pal *palette) {
	shortcuts := []struct{ label, keys string }{
		{"Command palette", shortcutKey(false, "K")},
		{"Settings", shortcutKey(false, ",")},
		{"New connection", shortcutKey(false, "N")},
		{"Rescan keys", shortcutKey(false, "R")},
		{"Focus key filter", shortcutKey(false, "F")},
		{"Save value", shortcutKey(false, "S")},
		{"Toggle sidebar", shortcutKey(false, "B")},
		{"Close tab", shortcutKey(true, "W")},
		{"Next / previous tab", "Ctrl+Tab / Ctrl+Shift+Tab"},
		{"Connect selected", shortcutKey(false, "Enter")},
	}
	a.prefGroup(c, pal, "Keyboard", func() {
		for _, s := range shortcuts {
			a.prefRow(c, pal, s.label, "", func() {
				ui.Text(c, s.keys).FontSize(11).TextColor(pal.muted).FontFeatures("tnum")
			})
		}
	})
	ui.Text(c, a.tr("Shortcuts are fixed for now; the command palette lists every action.")).
		FontSize(10.5).TextColor(pal.faint).Padding(0, 4)
}

func (a *App) viewDecoderPreferences(c *ui.Context, pal *palette) {
	a.prefGroup(c, pal, "Custom decoders", func() {
		if len(a.prefs.CustomDecoders) == 0 && !a.decoderEdit {
			ui.Text(c, a.tr("No custom decoders.")).FontSize(11).TextColor(pal.faint).Padding(8, 0)
		} else {
			for i, d := range a.prefs.CustomDecoders {
				ui.Row(c).Height(38).Padding(4, 6).Gap(6).Radius(6).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
						ui.Text(c, d.Name).FontSize(11).FontWeight(600).TextColor(pal.text).SingleLine()
						ui.Text(c, d.Command).Font("monospace").FontSize(9).TextColor(pal.faint).
							SingleLine().Ellipsis("…").Tooltip(d.Command)
					})
					iconButton(c, "decoder-edit-"+d.Name, iconPencil, a.tr("Edit decoder"), pal, func() {
						a.openDecoderEditor(i)
					})
					iconButton(c, "decoder-delete-"+d.Name, iconTrash, a.tr("Delete decoder"), pal, func() {
						a.deleteDecoder(i)
					})
				})
			}
		}
		if !a.decoderEdit {
			ui.Row(c).Padding(6, 0, 8, 0).Children(func() {
				if textButton(c, "decoder-add", a.tr("Add decoder"), false, false, pal).Clicked() {
					a.openDecoderEditor(-1)
				}
			})
		}
	})
	if a.decoderEdit {
		a.prefGroup(c, pal, "Custom decoder", func() {
			ui.Column(c).Gap(8).Padding(8, 0).Children(func() {
				ui.Field(c, a.tr("Name"), func() {
					ui.TextInput(c, &a.decoderName).Width(280)
				})
				ui.Field(c, a.tr("Decode command"), func() {
					ui.TextInput(c, &a.decoderCmd).Placeholder("decoder --option").Width(390)
				})
				ui.Field(c, a.tr("Encode command (optional)"), func() {
					ui.TextInput(c, &a.decoderEncode).Placeholder("encoder --option").Width(390)
				})
				ui.Text(c, a.tr("Reads the value on stdin. Only run programs you trust.")).
					FontSize(9.5).TextColor(pal.faint).LineHeight(1.3)
				if a.decoderErr != "" {
					ui.Text(c, a.tr(a.decoderErr)).FontSize(10.5).TextColor(pal.danger)
				}
				ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
					if textButton(c, "decoder-cancel", a.tr("Cancel"), false, false, pal).Clicked() {
						a.decoderEdit = false
					}
					if textButton(c, "decoder-save", a.tr("Save"), true, false, pal).
						Disabled(strings.TrimSpace(a.decoderName) == "" || strings.TrimSpace(a.decoderCmd) == "").Clicked() {
						a.saveDecoder()
					}
				})
			})
		})
	} else {
		ui.Text(c, a.tr("Named decoders appear as Custom: <name> in the value toolbar.")).
			FontSize(10).TextColor(pal.faint).Padding(0, 4)
	}
}

func (a *App) openDecoderEditor(index int) {
	a.decoderEdit = true
	a.decoderIndex = index
	a.decoderErr = ""
	if index >= 0 && index < len(a.prefs.CustomDecoders) {
		d := a.prefs.CustomDecoders[index]
		a.decoderName, a.decoderCmd, a.decoderEncode = d.Name, d.Command, d.EncodeCommand
	} else {
		a.decoderName, a.decoderCmd, a.decoderEncode = "", "", ""
	}
}

func (a *App) saveDecoder() {
	name, command := strings.TrimSpace(a.decoderName), strings.TrimSpace(a.decoderCmd)
	if name == "" || command == "" {
		a.decoderErr = "Name and command are required"
		return
	}
	for i, d := range a.prefs.CustomDecoders {
		if i != a.decoderIndex && d.Name == name {
			a.decoderErr = "A decoder with that name already exists"
			return
		}
	}
	oldName := ""
	if a.decoderIndex >= 0 && a.decoderIndex < len(a.prefs.CustomDecoders) {
		oldName = a.prefs.CustomDecoders[a.decoderIndex].Name
		a.prefs.CustomDecoders[a.decoderIndex] = CustomDecoder{Name: name, Command: command, EncodeCommand: strings.TrimSpace(a.decoderEncode)}
	} else {
		a.prefs.CustomDecoders = append(a.prefs.CustomDecoders, CustomDecoder{Name: name, Command: command, EncodeCommand: strings.TrimSpace(a.decoderEncode)})
	}
	if oldName != "" && a.prefs.DefaultDecode == "Custom: "+oldName {
		a.prefs.DefaultDecode = "Custom: " + name
	}
	for _, s := range a.sessions {
		if s.valDecode == "Custom: "+oldName {
			s.valDecode = "Custom: " + name
		}
		if s.valDecode == "Custom: "+name || (oldName != "" && s.valDecode == "Custom: "+oldName) {
			s.prepareDecodedEditor(s.rawString())
		}
	}
	a.decoderEdit = false
	a.savePrefs()
}

func (a *App) deleteDecoder(index int) {
	if index < 0 || index >= len(a.prefs.CustomDecoders) {
		return
	}
	name := a.prefs.CustomDecoders[index].Name
	a.prefs.CustomDecoders = append(a.prefs.CustomDecoders[:index], a.prefs.CustomDecoders[index+1:]...)
	if a.prefs.DefaultDecode == "Custom: "+name {
		a.prefs.DefaultDecode = "None"
	}
	for _, s := range a.sessions {
		if s.valDecode == "Custom: "+name {
			s.valDecode = "None"
		}
	}
	if a.decoderEdit {
		a.decoderEdit = false
	}
	a.savePrefs()
}

type paletteCommand struct {
	id, title, detail, shortcut string
	run                         func()
}

func (a *App) paletteCommands() []paletteCommand {
	cmds := []paletteCommand{
		{"new", a.tr("New connection"), a.tr("Open the connection editor"), shortcutKey(false, "N"), func() {
			a.stopOtherLive(-1)
			a.active = -1
			a.newConnection()
		}},
		{"prefs", a.tr("Preferences"), a.tr("Appearance, defaults and updates"), shortcutKey(false, ","), a.openPreferences},
		{"update", a.tr("Check for updates"), a.tr("Look for a newer GitHub release"), "", a.checkUpdates},
		{"theme-system", a.tr("Use system theme"), "", "", func() { a.prefs.Theme = "System"; a.savePrefs() }},
		{"theme-light", a.tr("Use light theme"), "", "", func() { a.prefs.Theme = "Light"; a.savePrefs() }},
		{"theme-dark", a.tr("Use dark theme"), "", "", func() { a.prefs.Theme = "Dark"; a.savePrefs() }},
	}
	if s := a.cur(); s != nil {
		cmds = append(cmds,
			paletteCommand{"rescan", a.tr("Rescan keys"), a.tr("Run SCAN again"), shortcutKey(false, "R"), s.rescan},
			paletteCommand{"filter", a.tr("Focus key filter"), "", shortcutKey(false, "F"), func() { a.prefs.SidebarHidden = false; s.focusSearch = true }},
			paletteCommand{"export", a.tr("Export keys as CSV"), "", "", func() { a.exportSessionCSV(s) }},
			paletteCommand{"close", a.tr("Close connection"), "", shortcutKey(true, "W"), func() { a.closeSession(a.active) }},
		)
		tools := []struct {
			id, title string
		}{
			{"browser", "Browser"}, {"status", "Status"}, {"cli", "CLI"},
			{"slowlog", "Slow log"}, {"monitor", "Monitor"},
			{"pubsub", "Pub/Sub"}, {"cmdlog", "Cmd log"},
		}
		for i, t := range tools {
			id := t.id
			cmds = append(cmds, paletteCommand{
				"tool-" + id, a.tr("Show " + t.title), "", shortcutKey(false, fmt.Sprint(i+1)), func() { s.setTool(id) },
			})
		}
	}
	cmds = append(cmds, paletteCommand{"sidebar", a.tr("Toggle sidebar"), "", shortcutKey(false, "B"), a.toggleSidebar})
	if s := a.cur(); s != nil {
		if !s.cfg.ReadOnly {
			cmds = append(cmds, paletteCommand{"import", a.tr("Import keys from CSV"), "", "", func() { a.importSessionCSV(s) }})
			cmds = append(cmds, paletteCommand{"new-key", a.tr("New key"), "", "", func() { a.openNewKey(s) }})
		}
		if s.valKey != "" {
			cmds = append(cmds, paletteCommand{"reload-value", a.tr("Reload value"), "", "", s.reloadValue})
			if s.canSaveValue() && s.editorModified() {
				cmds = append(cmds, paletteCommand{"save-value", a.tr("Save value"), "", shortcutKey(false, "S"), func() { s.saveString() }})
			}
		}
	}
	return append(cmds, a.navigationCommands()...)
}

func (a *App) filteredPalette() []paletteCommand {
	q := strings.ToLower(strings.TrimSpace(a.paletteQuery))
	var out []paletteCommand
	for _, cmd := range a.paletteCommands() {
		if q == "" || strings.Contains(strings.ToLower(cmd.title+" "+cmd.detail), q) {
			out = append(out, cmd)
			if len(out) == 40 {
				break
			}
		}
	}
	return out
}

func (a *App) viewPalette(c *ui.Context) {
	if !a.paletteOpen {
		return
	}
	pal := a.paletteFor(c)
	modal := ui.Modal(c.Key("palette-modal"), &a.paletteOpen, func() {
		ui.Column(c).Width(520).Padding(4).Gap(8).Children(func() {
			ui.Text(c, a.tr("Command palette")).FontSize(15).FontWeight(650).TextColor(pal.text)
			input := a.searchField(c, pal, &a.paletteQuery, "Search commands, connections or recent keys").AutoFocus()
			cmds := a.filteredPalette()
			if input.Changed() {
				a.paletteIndex, a.paletteReveal = 0, true
			}
			a.paletteIndex = max(0, min(a.paletteIndex, len(cmds)-1))
			if len(cmds) == 0 {
				ui.Text(c, a.tr("No matching commands")).FontSize(11.5).TextColor(pal.faint).Padding(8, 4)
			} else {
				ui.Scroll(c).MaxHeight(340).Children(func() {
					ui.Column(c).Gap(2).Children(func() {
						for i, cmd := range cmds {
							row := ui.Row(c.Key("cmd-"+cmd.id)).Height(34).Padding(0, 8).Gap(8).
								Radius(6).AlignItems(ui.Center).FocusRing(false)
							if i == a.paletteIndex {
								row.Background(pal.chip)
								if a.paletteReveal {
									row.ScrollIntoView()
								}
							} else if row.Hovered() {
								row.Background(pal.rowHover)
							}
							row.Children(func() {
								ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
									ui.Text(c, a.tr(cmd.title)).FontSize(12).FontWeight(550).TextColor(pal.text).SingleLine()
									if cmd.detail != "" {
										ui.Text(c, a.tr(cmd.detail)).FontSize(11).TextColor(pal.faint).SingleLine().Ellipsis("…")
									}
								})
								if cmd.shortcut != "" {
									ui.Text(c, cmd.shortcut).FontSize(10).TextColor(pal.faint)
								}
							})
							row.OnClick(func() {
								a.paletteOpen = false
								cmd.run()
							})
						}
					})
				})
			}
			a.paletteReveal = false
			ui.Text(c, a.tr("↑ ↓ to choose · Enter to open · Esc to close")).FontSize(11).TextColor(pal.muted)
			if input.Submitted() && len(cmds) > 0 {
				a.paletteOpen = false
				cmds[a.paletteIndex].run()
			}
		})
	})
	if modal.OverlayShortcut(0, ui.KeyDown) {
		a.paletteIndex = min(a.paletteIndex+1, len(a.filteredPalette())-1)
		a.paletteReveal = true
	}
	if modal.OverlayShortcut(0, ui.KeyUp) {
		a.paletteIndex = max(0, a.paletteIndex-1)
		a.paletteReveal = true
	}
	if modal.OverlayShortcut(ui.Cmd, ui.KeyK) || modal.OverlayShortcut(ui.Cmd|ui.Shift, ui.KeyP) {
		a.paletteOpen = false
	}
	if modal.OverlayShortcut(ui.Cmd, ui.KeyComma) {
		a.openPreferences()
	}
}

// handleShortcuts runs the global keyboard shortcuts. It is called before
// the frame is built, so a shortcut can change what the frame shows.
func (a *App) handleShortcuts(c *ui.Context) {
	if a.editGuard != nil || a.confirmOpen || a.rowConfirmOpen || a.slowResetOpen ||
		a.renameOpen || a.ttlOpen || a.newKeyOpen || a.rowOpen || a.csvImportOpen ||
		a.csvExportOpen || a.groupDestroyOpen || a.connDeleteOpen || a.groupRenameOpen {
		return
	}
	if c.Shortcut(ui.Cmd, ui.KeyK) || c.Shortcut(ui.Cmd|ui.Shift, ui.KeyP) {
		a.togglePalette()
		return
	}
	if c.Shortcut(ui.Cmd, ui.KeyComma) {
		a.openPreferences()
		return
	}
	if a.prefsOpen || a.paletteOpen {
		return
	}
	if c.Shortcut(ui.Cmd, ui.KeyB) {
		a.toggleSidebar()
		return
	}
	if c.Shortcut(ui.Cmd, ui.KeyN) {
		a.stopOtherLive(-1)
		a.active = -1
		a.newConnection()
		return
	}
	if a.cur() == nil {
		if c.Shortcut(ui.Cmd, ui.KeyUp) {
			a.selectConnOffset(-1)
			return
		}
		if c.Shortcut(ui.Cmd, ui.KeyDown) {
			a.selectConnOffset(1)
			return
		}
		if c.Shortcut(ui.Cmd, ui.KeyEnter) {
			a.connectSelected()
			return
		}
	}
	if len(a.sessions) > 0 {
		if c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyTab) {
			a.cycleSession(-1)
			return
		}
		if c.Shortcut(ui.Ctrl, ui.KeyTab) {
			a.cycleSession(1)
			return
		}
	}
	s := a.cur()
	if s == nil {
		return
	}
	if s.valueFull && c.Shortcut(0, ui.KeyEscape) {
		s.valueFull = false
		return
	}
	if c.Shortcut(ui.Cmd, ui.KeyR) {
		s.rescan()
		return
	}
	if c.Shortcut(ui.Cmd, ui.KeyF) {
		a.prefs.SidebarHidden = false
		s.focusSearch = true
		return
	}
	if c.Shortcut(ui.Cmd, ui.KeyS) {
		if s.canSaveValue() && s.editorModified() {
			s.saveString()
		}
		return
	}
	if c.Shortcut(ui.Cmd|ui.Shift, ui.KeyW) {
		a.closeSession(a.active)
		return
	}
	toolKeys := []struct {
		key ui.Key
		id  string
	}{
		{ui.Key1, "browser"}, {ui.Key2, "status"}, {ui.Key3, "cli"}, {ui.Key4, "slowlog"},
		{ui.Key5, "monitor"}, {ui.Key6, "pubsub"}, {ui.Key7, "cmdlog"},
	}
	for _, t := range toolKeys {
		if c.Shortcut(ui.Cmd, t.key) {
			s.setTool(t.id)
			return
		}
	}
}

// shortcutKey labels a shortcut for the platform.
func shortcutKey(shift bool, key string) string {
	if runtime.GOOS == "darwin" {
		if shift {
			return "⇧⌘" + key
		}
		return "⌘" + key
	}
	if shift {
		return "Ctrl+Shift+" + key
	}
	return "Ctrl+" + key
}

func (a *App) viewBehaviorPreferences(c *ui.Context, pal *palette) {
	a.prefGroup(c, pal, "Browsing", func() {
		for _, f := range []struct {
			label string
			value *float64
			max   float64
		}{
			{"Scan size", &a.prefs.ScanSize, 100000},
			{"Value page size", &a.prefs.ValuePageSize, 10000},
			{"Refresh interval (seconds)", &a.prefs.RefreshSeconds, 3600},
		} {
			a.prefRow(c, pal, f.label, "", func() {
				if ui.NumberInput(c.Key(f.label), f.value, 1, f.max, 1).Changed() {
					a.savePrefs()
				}
			})
		}
	})
	a.prefGroup(c, pal, "Editor & CLI", func() {
		for _, f := range []struct {
			label string
			font  *string
			size  *float64
		}{
			{"Editor font", &a.prefs.EditorFont, &a.prefs.EditorSize},
			{"CLI font", &a.prefs.CLIFont, &a.prefs.CLISize},
		} {
			a.prefRow(c, pal, f.label, "", func() {
				ui.Row(c.Key(f.label)).Gap(6).Children(func() {
					if ui.TextInput(c, f.font).Width(105).Changed() {
						a.savePrefs()
					}
					if ui.NumberInput(c, f.size, 8, 40, 1).Changed() {
						a.savePrefs()
					}
				})
			})
		}
		for _, f := range []struct {
			label string
			on    *bool
		}{
			{"Line numbers", a.prefs.LineNumbers}, {"Code folding", a.prefs.Folding}, {"Soft wrap", &a.prefs.SoftWrap},
		} {
			a.prefSwitch(c, pal, f.label, f.on, a.savePrefs)
		}
	})
}

func (a *App) configureEditor(state *editor.State) {
	state.Font = ui.Font{Family: a.prefs.EditorFont, Size: float32(a.prefs.EditorSize)}
	state.SoftWrap = a.prefs.SoftWrap
	state.LineNumbers = editor.LineNumbersOn
	if a.prefs.LineNumbers != nil && !*a.prefs.LineNumbers {
		state.LineNumbers = editor.LineNumbersOff
	}
	state.NoFolding = a.prefs.Folding != nil && !*a.prefs.Folding
}

package app

// Portions of the desktop layout and controls are adapted from kero.
// Copyright (c) 2026 EGOIST. MIT License; see third_party/licenses/kero.txt.

import (
	"fmt"
	"runtime"
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	topBarH     = 48
	sidebarW    = 280
	cardGap     = 8
	cardRadius  = 14
	cardHeaderH = 48
)

// Sidebar slide/fade follows kero/view.go at 0d6a7e4 (MIT).
var sidebarMotion = ui.Animation{Duration: 260 * time.Millisecond, Ease: ui.CubicBezier(0.25, 1, 0.5, 1)}

// resizeSidebar puts an invisible drag target on the working card's left edge.
func (a *App) resizeSidebar(c *ui.Context, pal *palette, maxWidth float32) {
	h := ui.Box(c.Key("sidebar-resize")).Absolute().Left(cardGap-4).Top(cardRadius).
		Bottom(cardGap+cardRadius).Width(9).Cursor(ui.CursorResizeColumn).
		Focusable().KeepFocus().FocusRing(false).Role(ui.RoleSplitter).
		Label(a.tr("Resize sidebar")).Range(240, float64(maxWidth), float64(a.prefs.SidebarWidth)).Step(10)
	setWidth := func(width float32) {
		width = min(max(width, 240), maxWidth)
		if width != a.prefs.SidebarWidth {
			a.prefs.SidebarWidth = width
			a.schedulePrefsSave()
		}
	}
	dx, _, held := h.Dragged()
	if held {
		if a.sidebarDrag == 0 {
			a.sidebarDrag = a.prefs.SidebarWidth
		}
		a.sidebarDrag += dx
		setWidth(a.sidebarDrag)
	} else {
		a.sidebarDrag = 0
	}
	if h.DoubleClicked() {
		a.sidebarDrag = 0
		setWidth(sidebarW)
	}
	h.OnShortcut(0, ui.KeyLeft, func() { setWidth(a.prefs.SidebarWidth - 10) })
	h.OnShortcut(0, ui.KeyRight, func() { setWidth(a.prefs.SidebarWidth + 10) })
	if h.Hovered() || held || h.FocusVisible() {
		color := pal.faint.Alpha(0.65)
		if held || h.FocusVisible() {
			color = pal.accent
		}
		h.Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X + (r.W-3)/2, Y: r.Y + (r.H-28)/2, W: 3, H: 28}, color, 1.5)
		})
	}
}

// viewTopBar draws the window's title bar: the connection tabs and the
// window's actions. It drags the window and leaves the traffic lights
// their room.
func (a *App) viewTopBar(c *ui.Context, pal *palette) {
	bar := c.TitleBar()
	h := float32(topBarH)
	if bar.Height > h {
		h = bar.Height
	}
	lead := bar.Left + 8
	if lead < 12 {
		lead = 12
	}
	ui.Row(c).Height(h).Shrink(0).Padding(0, 12, 0, lead).Gap(9).
		AlignItems(ui.Center).DragWindow().Children(func() {
		label := "Hide sidebar"
		if a.prefs.SidebarHidden {
			label = "Show sidebar"
		}
		iconButton(c, "toggle-sidebar", iconSidebar, a.tr(label), pal, a.toggleSidebar).
			Tooltip(a.tr(label) + " (" + shortcutKey(false, "B") + ")")
		if len(a.sessions) > 0 {
			ui.Row(c).Grow(1).MinWidth(0).Gap(2).ClipX().Children(func() {
				for i, s := range a.sessions {
					a.tab(c, pal, i, s)
				}
				iconButton(c, "tab-new", iconPlus, a.tr("New Connection"), pal, func() {
					a.stopOtherLive(-1)
					a.active = -1
					a.newConnection()
				})
			})
			if s := a.cur(); s != nil {
				iconButton(c, "top-refresh", iconRefresh, a.tr("Rescan"), pal, s.rescan)
			}
		} else {
			ui.Spacer(c)
		}
		a.viewUpdateNotice(c, pal)
		iconButton(c, "prefs", iconSettings, a.tr("Preferences"), pal, a.openPreferences)
		a.windowControls(c, pal)
	})
}

// tab is one open connection in the title bar.
func (a *App) tab(c *ui.Context, pal *palette, i int, s *session) {
	active := a.active == i
	t := ui.Row(c.Key("tab-"+s.id)).Height(28).Padding(0, 6).Radius(7).Gap(5).
		AlignItems(ui.Center).Shrink(0).MaxWidth(300)
	switch {
	case active:
		t.Background(pal.card).Shadow(0, 0.5, 2, 0, pal.shadow)
	case t.Hovered():
		t.Background(pal.rowHover)
	}
	text := pal.muted
	if active {
		text = pal.text
	}
	closed := false
	t.Children(func() {
		dot := pal.success
		switch {
		case s.statusErr:
			dot = pal.danger
		case s.scanning:
			dot = pal.warning
		}
		statusDot(c, dot)
		a.connectionBadges(c, pal, s.cfg.ConnectionOptions)
		ui.Text(c, s.name()).FontSize(12).FontWeight(500).TextColor(text).
			SingleLine().Ellipsis("…").MaxWidth(135).MinWidth(35).Tooltip(s.name())
		b := ui.ButtonBase(c.Key("tab-close-"+s.id)).Size(16, 16).Radius(4).Center().
			Label(a.tr("Close %s", s.name())).Tooltip(a.tr("Close %s", s.name()))
		if b.Hovered() || b.Pressed() {
			b.Background(pal.rowHover).TextColor(pal.text)
		} else {
			b.TextColor(pal.faint)
		}
		b.Children(func() {
			ui.Icon(c, iconClose).Size(10, 10)
		})
		if b.Clicked() {
			closed = true
			a.closeSession(i)
		}
	})
	if t.Clicked() && !closed {
		if a.active != i {
			a.stopOtherLive(i)
		}
		a.active = i
	}
}

// windowControls draws minimize, maximize and close where the platform
// does not keep its own, as Kero does.
func (a *App) windowControls(c *ui.Context, pal *palette) {
	if runtime.GOOS == "darwin" || a.win == nil {
		return
	}
	ui.Row(c).Gap(2).AlignItems(ui.Center).Shrink(0).Margin(0, 0, 0, 2).Children(func() {
		iconButton(c, "win-min", iconMinimize, a.tr("Minimize"), pal, a.win.Minimize)
		maximized := a.win.IsMaximized()
		glyph, label := iconMaximize, "Maximize"
		if maximized {
			glyph, label = iconRestore, "Restore"
		}
		label = a.tr(label)
		b := ui.ButtonBase(c.Key("win-max")).Size(28, 26).Radius(6).Center().Label(label).Tooltip(label)
		if b.Hovered() {
			b.Background(pal.rowHover)
		}
		b.TextColor(pal.muted).Children(func() {
			ui.Icon(c, glyph).Size(14, 14)
		})
		if b.Clicked() {
			if maximized {
				a.win.Unmaximize()
			} else {
				a.win.Maximize()
			}
		}
		b = ui.ButtonBase(c.Key("win-close")).Size(28, 26).Radius(6).Center().Label(a.tr("Close")).Tooltip(a.tr("Close"))
		if b.Hovered() || b.Pressed() {
			b.Background(pal.danger).TextColor(ui.Hex("#ffffff"))
		} else {
			b.TextColor(pal.muted)
		}
		b.Children(func() {
			ui.Icon(c, iconClose).Size(14, 14)
		})
		if b.Clicked() {
			a.win.Close()
		}
	})
}

// iconButton is a quiet square button for a toolbar.
func iconButton(c *ui.Context, key string, svg *ui.SVG, label string, pal *palette, fn func()) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Size(28, 26).Radius(6).Center().
		Label(label).Tooltip(label).Border(0, ui.Transparent)
	switch {
	case b.Pressed():
		b.Background(pal.rowHover.Mix(pal.text, 0.06)).TextColor(pal.text)
	case b.Hovered():
		b.Background(pal.rowHover).TextColor(pal.text)
	default:
		b.TextColor(pal.muted)
	}
	b.Children(func() {
		ui.Icon(c, svg).Size(15, 15)
	})
	if b.Clicked() {
		fn()
	}
	return b
}

func helpButton(c *ui.Context, key, label, detail string, pal *palette) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Size(24, 24).Radius(12).Center().FontSize(14).
		Border(1, pal.muted.Alpha(0.3)).TextColor(pal.muted).Label(label).Tooltip(detail)
	if b.Hovered() {
		b.Background(pal.rowHover).TextColor(pal.text)
	}
	b.Children(func() {
		ui.Text(c, "?").FontWeight(600)
	})
	return b
}

// textButton is the app's button: a filled control, the accent for a
// primary action, or the danger's tint for a destructive one.
func textButton(c *ui.Context, key, label string, primary, danger bool, pal *palette) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Height(30).Padding(0, 13).Radius(8).
		AlignItems(ui.Center).Label(label).Border(0, ui.Transparent)
	bg, fg := pal.chip, pal.text
	switch {
	case primary:
		bg, fg = pal.accent, pal.onAccent
	case danger:
		bg, fg = pal.danger.Alpha(0.1), pal.danger
	}
	switch {
	case b.Pressed():
		bg = bg.Mix(ui.Hex("#000000"), 0.09)
	case b.Hovered() && primary:
		bg = pal.accent.Mix(ui.Hex("#ffffff"), 0.08)
	case b.Hovered():
		bg = bg.Mix(pal.text, 0.05)
	}
	b.Background(bg).TextColor(fg)
	b.Children(func() {
		ui.Text(c, label).FontSize(12.5).FontWeight(550).SingleLine()
	})
	return b
}

// smallBadge labels a connection's capabilities, such as TLS or SSH.
func smallBadge(c *ui.Context, label string, pal *palette) ui.Element {
	return ui.Text(c, label).FontSize(11).FontWeight(600).TextColor(pal.muted).
		Padding(1, 4).Radius(4).Background(pal.chip)
}

// emptyState fills a card with an icon, a title, a detail and an action.
func emptyState(c *ui.Context, pal *palette, icon *ui.SVG, title, detail, actionLabel string, action func()) {
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Center().Padding(24).Children(func() {
		ui.Box(c).Size(48, 48).Radius(12).Background(pal.chip).Center().Children(func() {
			ui.Icon(c, icon).Size(22, 22).TextColor(pal.muted)
		})
		ui.Text(c, title).FontSize(17).FontWeight(650).TextColor(pal.text).Margin(16, 0, 0, 0)
		ui.Text(c, detail).FontSize(12.5).TextColor(pal.muted).TextAlign(ui.Center).
			MaxWidth(360).LineHeight(1.4).Margin(6, 0, 0, 0)
		if action != nil && actionLabel != "" {
			ui.Row(c).Margin(16, 0, 0, 0).Children(func() {
				if textButton(c, "empty-action", actionLabel, true, false, pal).Clicked() {
					action()
				}
			})
		}
	})
}

// cardTitle is the heading at the top of a card, with optional trailing
// content built by actions.
func cardTitle(c *ui.Context, pal *palette, title, detail string, actions func()) {
	ui.Row(c).Height(cardHeaderH).Shrink(0).Padding(0, 14).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).FontSize(14).FontWeight(600).TextColor(pal.text).SingleLine()
		if detail != "" {
			ui.Text(c, detail).FontSize(11.5).TextColor(pal.faint).SingleLine().Ellipsis("…")
		}
		ui.Spacer(c)
		if actions != nil {
			actions()
		}
	})
}

// menuItem is one row of a popover menu.
func menuItem(c *ui.Context, pal *palette, label string, icon *ui.SVG, danger bool, fn func()) ui.Element {
	row := ui.Row(c.Key("menu-"+label)).Height(30).Padding(6, 9).Gap(8).Radius(6).AlignItems(ui.Center)
	if row.Hovered() {
		row.Background(pal.rowHover)
	}
	color := pal.text
	if danger {
		color = pal.danger
	}
	row.Children(func() {
		ui.Icon(c, icon).Size(14, 14).TextColor(pal.muted)
		ui.Text(c, label).FontSize(12.5).TextColor(color).SingleLine()
	})
	return row.OnClick(fn)
}

// formatBytes prints a byte count with a compact unit.
func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

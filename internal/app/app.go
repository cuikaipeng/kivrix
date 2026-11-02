package app

// Portions of the desktop layout and controls are adapted from kero.
// Copyright (c) 2026 EGOIST. MIT License; see third_party/licenses/kero.txt.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

const (
	keyTreeDelim = ":"
	// scanDebounce keeps typing in the filter from starting a SCAN per key.
	scanDebounce = 250 * time.Millisecond
)

// App is the window's state: the saved profiles, the open sessions and the
// transient confirmations. Each session owns its connection, keyspace and
// value panel; views build the active one.
type App struct {
	win *mygo.Window
	// Headless flow tests supply the same queued, single-thread dispatch as Window.Update.
	dispatch          func(func())
	closed, suspended bool

	prefs             Preferences
	prefsPath         string
	sidebarDrag       float32
	prefsOpen         bool
	prefsTab          int
	settingsWin       *mygo.Window
	decoderEdit       bool
	decoderIndex      int
	decoderName       string
	decoderCmd        string
	decoderEncode     string
	decoderErr        string
	paletteOpen       bool
	paletteQuery      string
	updateChecking    bool
	updateStatus      message
	availableUpdate   *appUpdate
	updateNotice      bool
	updateDownloading bool
	updateCancel      context.CancelFunc
	updateReady       bool
	updateBytes       int64
	updateTotal       int64
	updateErr         string

	// Connections: the saved profiles, the editor form and the list.
	profiles          []Profile
	profilesPath      string
	form              connForm
	masterDiscoveryID uint64
	selProfile        string // profile ID in the form, "" for a new one
	connSel           int
	connQuery         string
	connScroll        ui.ScrollState
	connCollapsed     map[string]bool
	connMenuOpen      bool
	connIO            bool
	connConnecting    bool
	connTesting       bool
	connTestResult    message
	connTestErr       bool
	restoring         bool
	groupCreating     bool
	groupRenameOpen   bool
	groupRenameFrom   string
	groupRenameTo     string
	groupRenameErr    string
	connDeleteOpen    bool
	connDeleteID      string
	csvImportOpen     bool
	csvImportID       string
	csvImportOpts     CSVImportOptions
	csvImportTTL      float64
	csvExportOpen     bool
	csvExportID       string
	csvExportKeys     []string
	csvExportTTL      bool
	groupDestroyOpen  bool
	groupDestroyID    string
	groupDestroyName  string
	connErr           message
	secretCache       map[string]SecretSet

	// Open connections, shown as tabs. active is -1 on the connection
	// screen, where the saved profiles are listed.
	sessions []*session
	active   int

	// The connection screen's status line.
	status    message
	statusErr bool

	// Confirmation for a destructive action.
	confirmOpen bool
	pending     pendingDelete

	// The session menu and the key dialogs.
	sessionMenuOpen bool
	modalSessionID  string
	renameOpen      bool
	renameBusy      bool
	renameFrom      string
	renameTo        string
	renameErr       string
	ttlOpen         bool
	ttlBusy         bool
	ttlKeys         []string
	ttlSeconds      float64
	ttlErr          string
	newKeyOpen      bool
	newKeyBusy      bool
	newKeyName      string
	newKeyType      string
	newKeyValue     string
	newKeyTTL       float64
	newKeyErr       string
	rowOpen         bool
	rowPinned       bool
	rowGeneration   uint64
	rowCancel       context.CancelFunc
	rowSessionID    string
	row             rowEdit
	rowConfirmOpen  bool
	rowConfirm      rowEdit
	slowResetOpen   bool
	slowResetID     string
	editGuard       *editGuard
	allowClose      bool
	confirmPhrase   string
	recentKeys      []recentKey
	paletteReveal   bool
	paletteIndex    int
	prefsSaveTimer  *time.Timer
	afterConnect    func(*session)
}

// pendingDelete is a key, a namespace or a checked set waiting for
// confirmation, with the session it belongs to.
type pendingDelete struct {
	sessionID string
	path      string
	namespace bool
	flush     bool
	keys      []string
}

func newApp() *App {
	a := &App{
		connSel:       -1,
		active:        -1,
		prefs:         defaultPreferences(),
		connCollapsed: map[string]bool{},
		secretCache:   map[string]SecretSet{},
	}
	a.form.reset()
	return a
}

// Keep option IDs stable while translating or formatting their labels. Binding
// a temporary display string loses changes when MyGo rebuilds the view.
func (a *App) selectOption(c *ui.Context, value *string, options []string, compact bool, labels ...func(string) string) ui.Element {
	pal := a.paletteFor(c)
	theme := c.Theme()
	label := func(value string) string { return a.tr(value) }
	if len(labels) > 0 {
		label = labels[0]
	}
	selectBox := ui.SelectBase(c, value)
	trigger := selectBox.Trigger.Row().Padding(4, 7).Gap(4).AlignItems(ui.Center).
		Radius(6).Border(1, pal.divider).Background(pal.control).TextColor(pal.text)
	fontSize, itemPaddingY, itemPaddingX := float32(11), float32(5), float32(8)
	if compact {
		trigger.Height(26)
	} else {
		// Match TextInput's typography and padding so form controls align.
		fontSize, itemPaddingY, itemPaddingX = theme.FontSize, theme.Space(1.5), theme.Space(2.5)
		trigger.Padding(itemPaddingY, itemPaddingX).Radius(theme.Radius)
	}
	trigger.Children(func() {
		ui.Text(c, label(*value)).FontSize(fontSize).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		ui.Icon(c, iconChevronDown).Size(fontSize-1, fontSize-1).Shrink(0).TextColor(pal.faint)
	})
	selectBox.Popup(func(panel ui.Element) {
		panel.Padding(4).Radius(7).Background(pal.card).Border(1, pal.divider)
		ui.Scroll(c).MaxHeight(280).Children(func() {
			for _, option := range options {
				item := selectBox.Item(option).Padding(itemPaddingY, itemPaddingX).Radius(4).Label(label(option))
				if item.Highlighted() {
					item.Background(pal.accent.Alpha(0.13))
				}
				item.Children(func() {
					ui.Text(c, label(option)).FontSize(fontSize).TextColor(pal.text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				})
			}
		})
	})
	return trigger
}

// update runs fn on the main thread and draws a new frame.
func (a *App) update(fn func()) {
	if a.dispatch != nil {
		a.dispatch(fn)
	} else if a.win != nil {
		a.win.Update(fn)
	}
}

// cur is the session shown, nil on the connection screen.
func (a *App) cur() *session {
	if a.active < 0 || a.active >= len(a.sessions) {
		return nil
	}
	return a.sessions[a.active]
}

func (a *App) sessionIndex(id string) int {
	for i, s := range a.sessions {
		if s.id == id {
			return i
		}
	}
	return -1
}

func (a *App) sessionByID(id string) *session {
	if i := a.sessionIndex(id); i >= 0 {
		return a.sessions[i]
	}
	return nil
}

func (a *App) findProfile(id string) (Profile, bool) {
	for _, p := range a.profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// rememberActiveSession records the active profile and DB for the
// "Last session" startup option. It writes only when they change.
func (a *App) rememberActiveSession() {
	if a.restoring {
		return
	}
	s := a.cur()
	if s == nil || s.closed {
		return
	}
	if a.prefs.LastProfile == s.id && a.prefs.LastDB == s.db {
		return
	}
	a.prefs.LastProfile, a.prefs.LastDB = s.id, s.db
	a.savePrefs()
}

// lastSessionConfig finds the last active profile and the config to reopen
// it, including its last selected DB.
func (a *App) lastSessionConfig() (Profile, ConnConfig, bool, error) {
	if a.prefs.Startup != "Last session" || a.prefs.LastProfile == "" {
		return Profile{}, ConnConfig{}, false, nil
	}
	p, ok := a.findProfile(a.prefs.LastProfile)
	if !ok {
		return Profile{}, ConnConfig{}, false, nil
	}
	secrets, err := a.profileSecrets(p)
	if err != nil {
		return p, ConnConfig{}, false, err
	}
	cfg := p.config(secrets)
	if p.mode() == "cluster" {
		cfg.DB = 0
	} else {
		cfg.DB = a.prefs.LastDB
	}
	return p, cfg, true, nil
}

// restoreLastSession reopens the profile and DB that were active when the
// app closed, when the startup preference asks for it.
func (a *App) restoreLastSession() {
	p, cfg, ok, err := a.lastSessionConfig()
	if err != nil {
		a.setStatusErr(localError("Could not reopen the last session: %s", err))
		return
	}
	if !ok {
		return
	}
	a.restoring = true
	a.setStatus("Opening the last session…")
	go func() {
		var st Store
		err := st.Connect(cfg)
		var info string
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			info, _ = st.ServerInfo(ctx)
			cancel()
		}
		a.update(func() {
			a.restoring = false
			if a.closed {
				st.Close()
				return
			}
			if err != nil {
				a.setStatusErr(localError("Could not reopen the last session: %s", err))
				return
			}
			s := a.openSession(p, st, info)
			a.clearStatus()
			s.refreshDatabases()
			s.startScan()
			a.rememberActiveSession()
		})
	}()
}

// openSession adds a connection, replacing one already open for the same
// profile, and makes it the active tab.
func (a *App) openSession(p Profile, st Store, info string) *session {
	if i := a.sessionIndex(p.ID); i >= 0 {
		a.closeSessionNow(i)
	}
	s := newSession(a, p, st, info)
	a.sessions = append(a.sessions, s)
	a.active = len(a.sessions) - 1
	a.stopOtherLive(a.active)
	a.rememberActiveSession()
	return s
}

// closeSession stops a connection and shows its neighbour.
func (a *App) closeSession(i int) {
	if i < 0 || i >= len(a.sessions) {
		return
	}
	s := a.sessions[i]
	a.protectEdits(s, func() { a.closeSessionNow(a.sessionIndex(s.id)) })
}

func (a *App) closeSessionNow(i int) {
	if i < 0 || i >= len(a.sessions) {
		return
	}
	s := a.sessions[i]
	s.close()
	a.sessions = append(a.sessions[:i], a.sessions[i+1:]...)
	switch {
	case len(a.sessions) == 0:
		a.active = -1
		if !a.restoring {
			a.prefs.LastProfile = ""
			a.prefs.LastDB = 0
			a.savePrefs()
		}
	case a.active == i:
		a.active = min(i, len(a.sessions)-1)
	case a.active > i:
		a.active--
	}
	if a.pending.sessionID == s.id {
		a.pending = pendingDelete{}
		a.confirmOpen = false
	}
	if a.modalSessionID == s.id {
		a.renameOpen, a.ttlOpen, a.newKeyOpen = false, false, false
		a.modalSessionID = ""
	}
	if a.rowSessionID == s.id {
		a.rowOpen, a.rowConfirmOpen = false, false
		a.rowPinned = false
		a.rowSessionID = ""
	}
	if a.slowResetID == s.id {
		a.slowResetOpen = false
		a.slowResetID = ""
	}
	if a.csvImportID == s.id {
		a.csvImportOpen = false
		a.csvImportID = ""
	}
	if a.csvExportID == s.id {
		a.csvExportOpen = false
		a.csvExportID = ""
	}
	if a.groupDestroyID == s.id {
		a.groupDestroyOpen = false
		a.groupDestroyID = ""
		a.groupDestroyName = ""
	}
	a.sessionMenuOpen = false
	a.stopOtherLive(a.active)
}

// stopOtherLive keeps monitor and pub/sub on the visible session only.
func (a *App) stopOtherLive(active int) {
	for i, s := range a.sessions {
		if i != active {
			s.stopLive()
			s.stopStatusSampling()
		}
	}
}

// paletteFor applies the user's appearance preference over the system one.
func (a *App) paletteFor(c *ui.Context) *palette {
	base := &lightPalette
	if a.prefs.Theme == "Dark" || (a.prefs.Theme != "Light" && c.Theme().Dark) {
		base = &darkPalette
	}
	p := *base
	p.accent = accentColor(a.prefs.Accent, p.dark)
	p.onAccent = ui.Hex("#ffffff")
	p.selection = p.accent.Alpha(0.22)
	if p.dark {
		p.selection = p.accent.Alpha(0.28)
	}
	return &p
}

func (a *App) windowVibrancy() mygo.Vibrancy {
	if a.prefs.translucent() {
		// WindowBackground is opaque on macOS; UnderWindow shows the
		// blurred desktop and windows behind our transparent wash.
		return mygo.VibrancyUnderWindow
	}
	return mygo.VibrancyNone
}

func (a *App) applyTheme() {
	// The native material must follow the same appearance as our UI colors.
	source := mygo.ThemeSource(strings.ToLower(a.prefs.Theme))
	if mygo.Theme.Source() != source {
		mygo.Theme.SetSource(source)
	}
}

func (a *App) applyVibrancy() {
	if a.win != nil {
		a.win.SetVibrancy(a.windowVibrancy())
	}
	if a.settingsWin != nil {
		a.settingsWin.SetVibrancy(a.windowVibrancy())
	}
}

// searchField is a search input whose placeholder comes from the app's
// dictionary, unlike MyGo's SearchField, which always says Search.
func (a *App) searchField(c *ui.Context, pal *palette, value *string, placeholder string) ui.Element {
	var in ui.Element
	row := ui.Row(c).Grow(1).Height(30).Padding(0, 9).Gap(6).Radius(8).
		Background(pal.control).Border(1, pal.cardBorder).AlignItems(ui.Center)
	row.Children(func() {
		ui.Icon(c, iconSearch).Size(13, 13).TextColor(pal.faint).Shrink(0)
		in = ui.TextInputBase(c, value).Label(a.tr(placeholder)).Placeholder(a.tr(placeholder)).FontSize(12).
			Grow(1).MinWidth(0)
	})
	if in.Focused() {
		row.Border(1, pal.accent)
	}
	return in
}

func (a *App) view(c *ui.Context) {
	a.handleShortcuts(c)
	a.rememberActiveSession()
	pal := a.paletteFor(c)
	c.SetTheme(borderless(c, pal))
	a.backdrop(c, pal)
	s := a.cur()
	width, _ := c.Size()
	sidebarMax := max(float32(240), min(float32(360), width-440))
	a.prefs.SidebarWidth = min(max(a.prefs.SidebarWidth, 240), sidebarMax)
	ui.Column(c).Fill().Children(func() {
		a.viewTopBar(c, pal)
		hidden := a.prefs.SidebarHidden || (s != nil && s.valueFull)
		target := float32(1)
		if hidden {
			target = 0
			a.sidebarDrag = 0
		}
		row := ui.Row(c.Key("main-split")).Grow(1).MinHeight(0).AlignItems(ui.Stretch)
		shown := row.AnimateTo("sidebar", target, sidebarMotion)
		visibleWidth := float32(math.Round(float64(a.prefs.SidebarWidth * shown)))
		row.Children(func() {
			ui.Row(c.Key("sidebar-clip")).Width(visibleWidth).Shrink(0).AlignItems(ui.Stretch).
				ClipX().Disabled(hidden).Children(func() {
				if visibleWidth > 0 {
					// Slide at the saved width so controls never squeeze or reflow.
					ui.Column(c.Key("sidebar")).Width(a.prefs.SidebarWidth).Shrink(0).
						Margin(0, 0, 0, visibleWidth-a.prefs.SidebarWidth).
						Opacity(0.4 + 0.6*shown).Children(func() { a.viewSidebar(c, pal, s) })
				}
			})
			resizeMax := float32(0)
			if !hidden && shown == 1 {
				resizeMax = sidebarMax
			}
			a.viewMain(c.Key("main"), pal, s, resizeMax)
		})
	})
	a.viewDialogs(c)
}

// viewSidebar is the connections column on the connection screen, the
// keyspace when a session is active. Like kero's left sidebar it sits
// directly on the window wash, not on an opaque card.
func (a *App) viewSidebar(c *ui.Context, pal *palette, s *session) {
	ui.Column(c).Fill().MinWidth(0).
		Padding(0, 0, cardGap, cardGap).Children(func() {
		if s != nil {
			a.viewKeySidebar(c, pal, s)
		} else {
			a.viewConnSidebar(c, pal)
		}
	})
}

// viewMain is the working card.
func (a *App) viewMain(c *ui.Context, pal *palette, s *session, sidebarMax float32) {
	ui.Column(c).Grow(1).MinWidth(0).Padding(0, cardGap, cardGap, cardGap).Children(func() {
		card := raised(ui.Column(c.Key("card")).Grow(1).MinHeight(0).Radius(cardRadius).
			Background(pal.card).Clip(), pal)
		card.Children(func() {
			if s != nil {
				a.viewWorkTabs(c, pal, s)
				a.viewTransferProgress(c, pal, s)
				if s.tool == "browser" {
					a.viewValueCard(c, pal, s)
				} else {
					a.viewTool(c, pal, s)
				}
			} else {
				a.viewConnCard(c, pal)
			}
			a.viewCardStatus(c, pal, s)
		})
		if sidebarMax > 0 {
			a.resizeSidebar(c, pal, sidebarMax)
		}
	})
	if s != nil {
		s.watchSelection()
	}
}

func (a *App) viewKeySidebar(c *ui.Context, pal *palette, s *session) {
	var more ui.Element
	ui.Row(c).Height(cardHeaderH).Shrink(0).Padding(0, 12).Gap(7).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(26, 26).Radius(7).Background(pal.chip).Center().Children(func() {
			ui.Icon(c, iconDatabase).Size(14, 14).TextColor(pal.muted)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, s.name()).FontSize(13).FontWeight(600).TextColor(pal.text).
				SingleLine().Ellipsis("…")
			if s.info != "" {
				ui.Text(c, s.info).FontSize(11).TextColor(pal.faint).SingleLine().Ellipsis("…")
			}
		})
		iconButton(c, "new-key", iconPlus, a.tr("New key"), pal, func() {
			a.openNewKey(s)
		}).Disabled(s.cfg.ReadOnly)
		more = iconButton(c, "session-more", iconMore, a.tr("More"), pal, func() {
			a.sessionMenuOpen = !a.sessionMenuOpen
		})
	})
	if more.Valid() && a.sessionMenuOpen {
		ui.Popover(c, more, &a.sessionMenuOpen, func() {
			a.sessionMenu(c, pal, s)
		})
	}
	if s.cfg.ReadOnly || (s.cfg.Environment != "" && s.cfg.Environment != "Unspecified") {
		ui.Row(c).Padding(0, 12, 6, 12).Gap(5).Children(func() { a.connectionBadges(c, pal, s.cfg.ConnectionOptions) })
	}
	ui.Column(c.Key("key-controls")).Shrink(0).Padding(0, 10, 8, 10).Gap(8).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("DB")).FontSize(11).FontWeight(650).TextColor(pal.faint)
			if a.selectOption(c.Key("database"), &s.dbChoice, s.dbChoices, false, s.dbOptionLabel).
				Height(30).Grow(1).MinWidth(90).Label(a.tr("Database")).Disabled(s.dbSwitching || s.ioBusy || s.keyBusy || s.store.IsCluster()).Changed() {
				s.selectDB()
			}
		})
		field := a.searchField(c, pal, &s.search, "Filter keys").FontSize(c.Theme().FontSize)
		if s.focusSearch {
			field = field.AutoFocus()
			s.focusSearch = false
		}
		if field.Changed() {
			s.scheduleScan()
		}
		a.viewKeyFilters(c, pal, s)
	})
	// The Outline draws its chosen row in the theme's accent. Give this
	// tree a white accent so the whole row, arrow included, is white; the
	// row itself paints no second background.
	base := c.Theme()
	side := *base
	side.Accent, side.AccentText = pal.card, pal.text
	side.SurfaceHover = pal.card
	side.Focus = ui.Transparent
	c.SetTheme(&side)
	ui.Outline(c, &s.outline, s.keys.rootPaths(), s.keys.childPaths, func(path string) {
		a.keyRow(c, pal, s, path)
	}).Grow(1).FocusRing(false)
	c.SetTheme(base)
	if keys := s.selectedKeys(); len(keys) > 1 || (s.checkMode && len(keys) > 0) {
		ui.Row(c).Height(30).Shrink(0).Padding(0, 10).Gap(2).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("%d selected", len(keys))).FontSize(11).TextColor(pal.text).Grow(1)
			iconButton(c, "sel-all", iconList, a.tr("Select all loaded keys"), pal, func() {
				for path, node := range s.keys.byPath {
					if node.isKey {
						s.sel.Add(path)
					}
				}
			})
			iconButton(c, "sel-ttl", iconClock, a.tr("Set TTL"), pal, func() { a.openTTL(s, keys) }).Disabled(s.cfg.ReadOnly)
			iconButton(c, "sel-delete", iconTrash, a.tr("Delete selected"), pal, func() { a.askDeleteKeys(s, keys) }).Disabled(s.cfg.ReadOnly)
			iconButton(c, "sel-clear", iconClose, a.tr("Clear selection"), pal, func() {
				a.protectEdits(s, func() {
					s.sel.Clear()
					s.clearValue()
				})
			})
		})
	}
	a.viewKeyFooter(c, pal, s)
}

func (a *App) viewKeyFooter(c *ui.Context, pal *palette, s *session) {
	count := a.tr("Loaded %d keys", s.keys.count)
	state, color := a.tr("Scan complete"), pal.muted
	if s.scanIncomplete {
		state = a.tr("Scan incomplete")
	}
	if s.scanning {
		state, color = a.tr("scanning…"), pal.warning
	}
	ui.Row(c).Height(48).Shrink(0).Padding(0, 10).Gap(5).AlignItems(ui.Center).Children(func() {
		statusDot(c, color)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, count).FontSize(11.5).TextColor(pal.text).SingleLine().Ellipsis("…").Tooltip(count)
			ui.Text(c, state).FontSize(11).TextColor(color).SingleLine()
		})
		checkLabel := "Select keys"
		if s.checkMode {
			checkLabel = "Exit check mode"
		}
		iconButton(c, "check-mode", iconCheck, a.tr(checkLabel), pal, s.toggleCheckMode)
		if s.scanning {
			iconButton(c, "cancel-scan", iconClose, a.tr("Cancel scan"), pal, s.stopScan)
		}
	})
}

// keyRow builds one outline row: a branch with its child count, or a key.
func (a *App) keyRow(c *ui.Context, pal *palette, s *session, path string) {
	n := s.keys.node(path)
	if n == nil {
		ui.Text(c, path).Padding(0, 6)
		return
	}
	selected := s.sel.Has(path)
	binary := !utf8.ValidString(path)
	row := ui.Row(c).Height(28).Padding(0, 6).Gap(6).Radius(6).AlignItems(ui.Center).Grow(1).FocusRing(false)
	text, iconColor := pal.text, pal.muted
	badge, badgeBg := pal.muted, pal.chip
	row.Children(func() {
		if s.checkMode && n.isKey {
			box := ui.Box(c).Size(14, 14).Radius(3).Border(1, pal.faint)
			if selected {
				box.Background(pal.accent).Border(0, ui.Transparent).Center()
			}
			box.Children(func() {
				if selected {
					ui.Icon(c, iconCheck).Size(9, 9).TextColor(pal.onAccent)
				}
			})
		}
		icon := iconKey
		color := iconColor
		if len(n.children) > 0 {
			icon = iconFolder
			color = iconColor.Alpha(0.8)
		}
		ui.Icon(c, icon).Size(13, 13).TextColor(color)
		ui.Text(c, displayKey(n.name)).FontSize(12.5).TextColor(text).Grow(1).SingleLine()
		if binary {
			ui.Text(c, "BIN").FontSize(11).FontWeight(650).TextColor(iconColor).Padding(1, 4).
				Radius(4).Background(badgeBg)
		}
		if len(n.children) > 0 {
			ui.Textf(c, "%d", n.keyCount).FontSize(11).FontWeight(600).
				TextColor(badge).Padding(1, 5).Radius(4).Background(badgeBg)
		}
	})
	if row.Clicked() {
		s.setTool("browser")
		if n.isKey {
			if s.checkMode {
				if s.sel.Has(path) {
					s.sel.Remove(path)
				} else {
					s.sel.Add(path)
				}
			} else {
				s.sel.Clear()
				s.sel.Add(path)
			}
		} else if !s.checkMode {
			s.sel.Clear()
		}
	}
	row.ContextMenu(func(m *ui.Menu) {
		if n.isKey {
			if m.Item(a.tr("Copy Key Name")).Chosen() {
				a.copy(c, s, n.path)
			}
			if !binary {
				if m.Item(a.tr("Rename…")).Disabled(s.cfg.ReadOnly).Chosen() {
					a.openRename(s, n.path)
				}
			}
			if m.Item(a.tr("Set TTL…")).Disabled(s.cfg.ReadOnly).Chosen() {
				a.openTTL(s, []string{n.path})
			}
			if m.Item(a.tr("Delete Key…")).Disabled(s.cfg.ReadOnly).Chosen() {
				a.askDeleteKey(s, n.path)
			}
		}
		if len(n.children) > 0 {
			if m.Item(a.tr("Delete Namespace…")).Disabled(s.cfg.ReadOnly).Chosen() {
				a.askDeleteNamespace(s, n.path)
			}
		}
	})
}

func (a *App) viewValueCard(c *ui.Context, pal *palette, s *session) {
	ui.Row(c).Height(cardHeaderH).Shrink(0).Padding(0, 14).Gap(8).AlignItems(ui.Center).Children(func() {
		if s.valKey == "" {
			ui.Text(c, a.tr("Value")).FontSize(14).FontWeight(600).TextColor(pal.text)
			ui.Text(c, a.tr("Select a key in the sidebar")).FontSize(11.5).TextColor(pal.faint).SingleLine()
		} else {
			if s.val.Type != "" && s.val.Type != "none" {
				typeTag(c, s.val.Type, pal)
			}
			ui.Text(c, displayKey(s.valKey)).FontSize(12.5).FontWeight(550).TextColor(pal.text).
				SingleLine().Ellipsis("…").Grow(1).Tooltip(displayKey(s.valKey))
			if s.val.TTL >= 0 {
				ui.Text(c, "TTL "+formatTTL(s.val.TTL)).FontSize(11).TextColor(pal.faint)
			}
		}
		ui.Spacer(c)
		if s.valKey != "" {
			if s.val.Type == "string" || s.val.Type == "json" {
				label := a.tr("Save")
				if s.valueSaving {
					label = a.tr("Saving…")
				}
				if s.editorModified() {
					smallBadge(c, a.tr("Unsaved"), pal).TextColor(pal.warning)
				}
				if textButton(c, "value-save", label, s.editorModified(), false, pal).
					Disabled(!s.canSaveValue() || !s.editorModified()).Clicked() {
					s.saveString()
				}
			}
			if !s.cfg.ReadOnly && s.val.Type != "stream" && canAddRow(s.val.Type) && s.valRowSel >= 0 {
				row := s.valRowSel
				iconButton(c, "value-edit-row", iconPencil, a.tr("Edit selected row"), pal, func() {
					a.openRowEdit(s, row)
				})
			}
			copyLabel := "Copy value"
			if s.val.PreviewOnly {
				copyLabel = "Copy preview"
			}
			iconButton(c, "value-copy", iconCopy, a.tr(copyLabel), pal, func() {
				a.copyValue(c, s)
			})
			fullIcon := iconMaximize
			if s.valueFull {
				fullIcon = iconRestore
			}
			iconButton(c, "value-fullscreen", fullIcon, a.tr("Toggle value fullscreen"), pal, func() {
				s.valueFull = !s.valueFull
			})
			iconButton(c, "value-reload", iconRefresh, a.tr("Reload value"), pal, func() {
				s.loadValue(s.valKey)
			})
		}
	})
	if s.val.Type != "" && s.val.Type != "none" {
		a.viewValueToolbar(c, pal, s)
	}
	a.viewValueBody(c, pal, s)
	if s.groupsOpen && s.val.Type == "stream" {
		a.viewStreamGroups(c, pal, s)
	}
	if s.trimOpen && s.val.Type == "stream" {
		a.viewStreamTrim(c, pal, s)
	}
}

// viewValueToolbar holds the value's filter, display format, decoder and
// paging controls.
func (a *App) viewValueToolbar(c *ui.Context, pal *palette, s *session) {
	ui.Row(c).Height(34).Shrink(0).Padding(0, 12).Gap(6).AlignItems(ui.Center).Children(func() {
		switch s.val.Type {
		case "hash":
			if a.searchField(c, pal, &s.valQuery, "Filter fields").Grow(1).MinWidth(80).Changed() {
				s.scheduleValueReload()
			}
		case "list":
			if a.searchField(c, pal, &s.valQuery, "Filter elements").Grow(1).MinWidth(80).Changed() {
				s.scheduleValueReload()
			}
		case "set", "zset":
			if a.searchField(c, pal, &s.valQuery, "Filter members").Grow(1).MinWidth(80).Changed() {
				s.scheduleValueReload()
			}
		case "stream":
			a.searchField(c, pal, &s.valQuery, "Filter entries").Grow(1).MinWidth(80)
			text := a.tr("%d entries · last %s", s.val.Size, s.val.LastID)
			if s.val.Groups > 0 {
				text += a.tr(" · %d groups", s.val.Groups)
			}
			ui.Text(c, text).FontSize(11).TextColor(pal.faint).SingleLine().Ellipsis("…").MaxWidth(300)
		default:
			ui.Spacer(c)
		}
		if a.selectOption(c, &s.valFormat, valueFormats, true).Disabled(s.editorModified() || s.valueSaving || s.val.PreviewOnly).Width(94).Label(a.tr("Format")).Tooltip(a.tr("Format")).Changed() {
			if s.val.Type == "string" || s.val.Type == "json" {
				s.prepareDecodedEditor(s.rawString())
			}
		}
		if a.selectOption(c, &s.valDecode, s.decodeChoices(), true).Disabled(s.editorModified() || s.valueSaving || s.val.PreviewOnly).Width(150).Label(a.tr("Decode")).Tooltip(a.tr("Decode")).Changed() {
			if s.val.Type == "string" || s.val.Type == "json" {
				s.prepareDecodedEditor(s.rawString())
			}
		}
		if s.val.Type == "stream" {
			iconButton(c, "value-groups", iconUsers, a.tr("Consumer groups"), pal, s.openStreamGroups)
			iconButton(c, "value-trim", iconScissors, a.tr("Trim stream"), pal, s.openStreamTrim).Disabled(s.cfg.ReadOnly)
		}
		if canAddRow(s.val.Type) && !s.cfg.ReadOnly {
			iconButton(c, "value-add", iconPlus, a.tr("Add row"), pal, func() {
				a.openRowAdd(s)
			})
		}
		if s.val.Type == "stream" && s.streamMore && !s.valLoading && !s.valLoadingMore {
			iconButton(c, "value-more", iconChevronDown, a.tr("Load more entries"), pal, s.loadMoreStream)
		} else if s.valMore && !s.valLoading && !s.valLoadingMore {
			iconButton(c, "value-more", iconChevronDown, a.tr("Load more rows"), pal, s.loadMoreValue)
		}
	})
}

func (a *App) viewValueBody(c *ui.Context, pal *palette, s *session) {
	switch {
	case s.valKey == "":
		emptyState(c, pal, iconKey, a.tr("Select a key"), a.tr("Choose a key in the sidebar to inspect its value."), "", nil)
	case s.valLoading:
		ui.Column(c).Grow(1).MinHeight(0).Center().Children(func() {
			ui.Spinner(c).Label(a.tr("Loading…"))
		})
	case s.valErr != "":
		ui.Column(c).Grow(1).MinHeight(0).Center().Padding(24).Children(func() {
			ui.Box(c).Size(44, 44).Radius(12).Background(pal.danger.Alpha(0.1)).Center().Children(func() {
				ui.Icon(c, iconAlert).Size(20, 20).TextColor(pal.danger)
			})
			ui.Text(c, a.tr("Could not load the value")).FontSize(16).FontWeight(650).TextColor(pal.text).Margin(12, 0, 0, 0)
			ui.Text(c, s.valErr).FontSize(12).TextColor(pal.muted).TextAlign(ui.Center).
				MaxWidth(420).LineHeight(1.4).Margin(5, 0, 0, 0)
			if textButton(c, "value-retry", a.tr("Retry"), true, false, pal).Margin(12, 0, 0, 0).Clicked() {
				s.loadValue(s.valKey)
			}
		})
	default:
		a.viewValue(c, pal, s)
	}
}

// viewValue shows the loaded value by type, through the chosen format and
// decoder.
func (a *App) viewValue(c *ui.Context, pal *palette, s *session) {
	a.configureEditor(&s.strEditor)
	a.configureEditor(&s.decodedEditor)
	s.strEditor.ReadOnly = s.cfg.ReadOnly || s.keyBusy || s.ioBusy || s.val.PreviewOnly
	s.decodedEditor.ReadOnly = s.cfg.ReadOnly || s.keyBusy || s.ioBusy || !s.editCodec.editable() || s.val.PreviewOnly
	if s.val.PreviewOnly {
		ui.Text(c, a.tr("Read-only preview: first %s of %s. Editing is limited to 1 MiB.", formatBytes(int64(len(s.val.Str))), formatBytes(s.val.Size))).
			FontSize(12).TextColor(pal.warning).Padding(8, 14)
	}
	switch s.val.Type {
	case "string", "json":
		if s.encodedEditorActive() {
			ui.Column(c).Grow(1).MinHeight(0).Children(func() {
				ui.Row(c).Padding(6, 14).Children(func() {
					ui.Text(c, a.tr("Decoded view · Save restores the selected encoding")).FontSize(11).TextColor(pal.faint).Grow(1)
					if textButton(c, "decoded-edit-raw", a.tr("Edit raw"), false, false, pal).Disabled(s.editorModified()).Clicked() {
						s.valFormat, s.valDecode = "Raw", "None"
						s.decodedID++
					}
				})
				switch {
				case s.decodedBusy:
					ui.Column(c).Grow(1).Center().Children(func() {
						ui.Spinner(c).Label(a.tr("Decoding…"))
					})
				case s.decodedErr != "":
					ui.Column(c).Grow(1).Center().Padding(24).Children(func() {
						ui.Text(c, s.decodedErr).FontSize(11.5).TextColor(pal.danger).TextAlign(ui.Center).MaxWidth(420)
						if textButton(c, "decoded-retry", a.tr("Retry"), true, false, pal).Margin(10, 0, 0, 0).Clicked() {
							s.prepareDecodedEditor(s.rawString())
						}
					})
				case s.decodedEditor.Buffer != nil:
					editor.View(c, &s.decodedEditor).Grow(1).MinHeight(0)
				}
			})
			return
		}
		if s.valFormat == "Raw" && s.valDecode == "None" {
			editor.View(c, &s.strEditor).Grow(1).MinHeight(0)
			return
		}
		ui.Column(c).Grow(1).MinHeight(0).Children(func() {
			ui.Row(c).Padding(6, 14).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.tr("Formatted view · choose Raw and None to edit")).FontSize(11).TextColor(pal.faint).Grow(1)
				if textButton(c, "value-edit-raw", a.tr("Edit raw"), false, false, pal).Clicked() {
					s.valFormat, s.valDecode = "Raw", "None"
					s.setStatus("Raw editor enabled")
				}
			})
			if s.decodedBusy {
				ui.Spinner(c).Label(a.tr("Decoding…"))
			} else if s.decodedErr != "" {
				ui.Text(c, s.decodedErr).FontSize(12).TextColor(pal.danger)
			} else if s.decodedEditor.Buffer != nil {
				s.decodedEditor.ReadOnly = true
				editor.View(c, &s.decodedEditor).Grow(1).MinHeight(0)
			}
		})
	case "hash":
		if s.valSort != s.lastValSort {
			s.sortHashRows()
		}
		cols := []ui.TableColumn{{ID: "field", Title: a.tr("Field"), Sortable: true}, {ID: "value", Title: a.tr("Value"), Sortable: true}}
		tbl := ui.Table(c, &s.valRows, cols, len(s.val.Pairs), func(row, col int) {
			text := s.val.Pairs[row][col]
			if col == 1 {
				text = s.cellValue(text)
			}
			text = limitedText(text, maxLiveText)
			ui.Text(c, text).SingleLine().Tooltip(text)
		}).Grow(1).Tooltip(a.tr("Double-click a row to edit it"))
		a.editOnSubmit(s, tbl)
		a.truncatedNote(c, pal, s)
	case "zset":
		cols := []ui.TableColumn{{Title: a.tr("Member")}, {Title: a.tr("Score"), Width: 120, Align: ui.End}}
		tbl := ui.Table(c, &s.valRows, cols, len(s.val.Pairs), func(row, col int) {
			text := s.val.Pairs[row][col]
			if col == 0 {
				text = s.cellValue(text)
			}
			ui.Text(c, text).SingleLine().Tooltip(text)
		}).Grow(1).Tooltip(a.tr("Double-click a row to edit it"))
		a.editOnSubmit(s, tbl)
		a.truncatedNote(c, pal, s)
	case "list", "set":
		title := "Value"
		if s.val.Type == "list" {
			title = "Element"
		}
		cols := []ui.TableColumn{{Title: "#", Width: 60, Align: ui.End}, {Title: a.tr(title)}}
		tbl := ui.Table(c, &s.valRows, cols, len(s.val.Items), func(row, col int) {
			if col == 0 {
				idx := row
				if row < len(s.valIndices) {
					idx = s.valIndices[row]
				}
				ui.Textf(c, "%d", idx).TextColor(pal.muted).SingleLine()
				return
			}
			text := s.cellValue(s.val.Items[row])
			ui.Text(c, text).SingleLine().Tooltip(text)
		}).Grow(1).Tooltip(a.tr("Double-click a row to edit it"))
		a.editOnSubmit(s, tbl)
		a.truncatedNote(c, pal, s)
	case "stream":
		indices := s.filteredStream()
		s.streamIndices = indices
		if len(indices) == 0 {
			emptyState(c, pal, iconSearch, a.tr("No matching entries"), "", "", nil)
			return
		}
		cols := []ui.TableColumn{{Title: a.tr("ID"), Width: 170}, {Title: a.tr("Fields")}}
		ui.Table(c, &s.valRows, cols, len(indices), func(row, col int) {
			e := s.val.Stream[indices[row]]
			switch col {
			case 0:
				ui.Text(c, e.ID).Font("monospace").FontSize(11).TextColor(pal.muted).SingleLine()
			case 1:
				var preview strings.Builder
				for _, f := range e.Fields {
					if preview.Len() >= maxLiveText {
						break
					}
					if preview.Len() > 0 {
						preview.WriteString(", ")
					}
					preview.WriteString(limitedText(f[0], maxLiveText))
					preview.WriteString("=")
					preview.WriteString(s.cellValue(f[1]))
				}
				text := limitedText(preview.String(), maxLiveText)
				ui.Text(c, text).FontSize(11.5).TextColor(pal.text).SingleLine().Tooltip(text)
			}
		}).Grow(1).Tooltip(a.tr("Newest stream entries first"))
		a.truncatedNote(c, pal, s)
	case "none":
		emptyState(c, pal, iconKey, a.tr("This key no longer exists"),
			a.tr("It may have expired or been deleted."), "", nil)
	default:
		emptyState(c, pal, iconAlert, a.tr("Unsupported type"),
			"Type “"+s.val.Type+"” is not displayed yet.", "", nil)
	}
}

// currentCustomDecoder returns the selected named decoder, if any.
func (s *session) currentCustomDecoder() (CustomDecoder, bool) {
	name, ok := strings.CutPrefix(s.valDecode, "Custom: ")
	if !ok {
		return CustomDecoder{}, false
	}
	return s.app.decoderByName(name)
}

// Formatted and decoded values use a separate buffer and a captured codec.
func (s *session) encodedEditorActive() bool {
	return (s.val.Type == "string" || s.val.Type == "json") &&
		((s.valFormat != "Raw" && s.valFormat != "") || (s.valDecode != "None" && s.valDecode != "")) && s.valueCodec().editable()
}

func (s *session) prepareDecodedEditor(raw string) {
	if s.decodedCancel != nil {
		s.decodedCancel()
	}
	s.decodedID++
	id := s.decodedID
	s.decodedBusy, s.decodedErr = false, ""
	if s.valFormat == "Raw" && s.valDecode == "None" {
		return
	}
	codec, key := s.valueCodec(), s.valKey
	ctx, cancel := context.WithCancel(context.Background())
	s.decodedCancel = cancel
	s.decodedBusy = true
	go func() {
		defer cancel()
		text, resolved, err := codec.openContext(ctx, raw)
		s.app.update(func() {
			if id != s.decodedID || s.closed || s.valKey != key {
				return
			}
			s.decodedBusy = false
			if err != nil {
				s.decodedErr = err.Error()
				return
			}
			s.editCodec = resolved
			s.decodedEditor.ReadOnly = !codec.editable()
			if s.decodedEditor.Buffer == nil {
				s.decodedEditor.Buffer = editor.NewBuffer(text)
			} else {
				s.decodedEditor.Buffer.Reset(text)
			}
			s.decodedEditor.Buffer.MarkSaved()
			s.decodedEditor.Buffer.SetLanguage(sniffLanguage(text))
		})
	}()
}

func (s *session) canSaveValue() bool {
	if s.valueSaving || s.valLoading || s.cfg.ReadOnly || s.keyBusy || s.ioBusy || s.val.PreviewOnly {
		return false
	}
	if s.val.Type != "string" && s.val.Type != "json" {
		return false
	}
	if s.valFormat == "Raw" && s.valDecode == "None" {
		return true
	}
	return s.encodedEditorActive() && !s.decodedBusy && s.decodedErr == "" && s.decodedEditor.Buffer != nil
}

func (s *session) editorModified() bool {
	if s.val.Type != "string" && s.val.Type != "json" {
		return false
	}
	buf := s.strEditor.Buffer
	if s.encodedEditorActive() {
		buf = s.decodedEditor.Buffer
	}
	return buf != nil && buf.Modified()
}

// decodeChoices includes the session's decoder even if its preset was removed.
func (s *session) decodeChoices() []string {
	out := s.app.decodeChoices()
	if s.valDecode != "" && !containsString(out, s.valDecode) {
		out = append(out, s.valDecode)
	}
	return out
}

// decodeCommand turns "Custom: name" into the command registered in
// Preferences, leaving built-in decoders untouched.
func (s *session) decodeCommand() (string, string) {
	name, ok := strings.CutPrefix(s.valDecode, "Custom: ")
	if !ok {
		return s.valDecode, ""
	}
	if command, found := s.app.decoderCommand(name); found {
		return "Custom", command
	}
	return "Custom", ""
}

func (s *session) cellValue(raw string) string {
	decode, command := s.decodeCommand()
	if (decode != "None" && decode != "") || (s.valFormat != "Raw" && s.valFormat != "") {
		return s.formattedCellValue(raw)
	}
	return cellValueCustom(limitedText(raw, maxLiveText), s.valFormat, decode, command)
}

func (s *session) rawString() string {
	if s.strEditor.Buffer != nil {
		return s.strEditor.Buffer.String()
	}
	return s.val.Str
}

// valueText is the raw value as copyable text.
func (s *session) valueText() string {
	switch s.val.Type {
	case "string", "json":
		if s.encodedEditorActive() && s.decodedEditor.Buffer != nil {
			return s.decodedEditor.Buffer.String()
		}
		if s.strEditor.Buffer != nil {
			return s.strEditor.Buffer.String()
		}
		return s.val.Str
	case "hash":
		m := make(map[string]string, len(s.val.Pairs))
		for _, p := range s.val.Pairs {
			m[p[0]] = p[1]
		}
		if b, err := json.MarshalIndent(m, "", "  "); err == nil {
			return string(b)
		}
		return ""
	case "list", "set":
		if b, err := json.MarshalIndent(s.val.Items, "", "  "); err == nil {
			return string(b)
		}
		return ""
	case "zset":
		rows := make([]csvZSet, 0, len(s.val.Pairs))
		for _, p := range s.val.Pairs {
			score, _ := strconv.ParseFloat(p[1], 64)
			rows = append(rows, csvZSet{Member: p[0], Score: score})
		}
		if b, err := json.MarshalIndent(rows, "", "  "); err == nil {
			return string(b)
		}
		return ""
	case "stream":
		rows := make([]csvStream, 0, len(s.val.Stream))
		for _, e := range s.val.Stream {
			fields := make(map[string]string, len(e.Fields))
			for _, f := range e.Fields {
				fields[f[0]] = f[1]
			}
			rows = append(rows, csvStream{ID: e.ID, Fields: fields})
		}
		if b, err := json.MarshalIndent(rows, "", "  "); err == nil {
			return string(b)
		}
		return ""
	}
	return ""
}

// copyValue puts the raw value on the clipboard.
func (a *App) copyValue(c *ui.Context, s *session) {
	text := s.valueText()
	if text == "" {
		s.setStatus("Nothing to copy")
		return
	}
	c.Services().WriteClipboard(text)
	if s.val.PreviewOnly {
		s.setStatus("Copied preview only")
	} else {
		s.setStatus("Copied value")
	}
}

func displayKey(key string) string {
	key = limitedText(key, maxLiveText)
	return strings.ToValidUTF8(key, "�")
}

// valLength prefers the server's total length over the rows on this page.
func (s *session) valLength(loaded int64) int64 {
	if s.val.Size > 0 {
		return s.val.Size
	}
	return loaded
}

// valMeta is the value's length and memory usage, for the header.
func (s *session) valMeta() string {
	var size string
	switch s.val.Type {
	case "string", "json":
		length := int64(len(s.val.Str))
		if s.val.PreviewOnly {
			length = s.val.Size
		}
		size = s.app.tr("%s bytes", formatCount(length))
	case "hash":
		size = s.app.tr("%s fields", formatCount(s.valLength(int64(len(s.val.Pairs)))))
	case "list":
		size = s.app.tr("%s items", formatCount(s.valLength(int64(len(s.val.Items)))))
	case "set":
		size = s.app.tr("%s members", formatCount(s.valLength(int64(len(s.val.Items)))))
	case "zset":
		size = s.app.tr("%s members", formatCount(s.valLength(int64(len(s.val.Pairs)))))
	case "stream":
		return ""
	}
	if size == "" {
		return ""
	}
	if s.val.Memory > 0 {
		size += " · " + formatBytes(s.val.Memory)
	}
	return size
}

// editOnSubmit opens the row dialog on a double click or Enter.
func (a *App) editOnSubmit(s *session, tbl ui.Element) {
	if s.val.Type == "stream" {
		return
	}
	if tbl.Submitted() && s.valRowSel >= 0 {
		a.openRowEdit(s, s.valRowSel)
	}
}

func (a *App) truncatedNote(c *ui.Context, pal *palette, s *session) {
	if !s.val.Truncated {
		return
	}
	ui.Row(c).Padding(6, 14).Children(func() {
		ui.Text(c, a.tr("Showing %d of %d rows", len(s.val.Pairs)+len(s.val.Items)+len(s.val.Stream), s.val.Size)).
			FontSize(11).TextColor(pal.faint)
	})
}

// askDeleteKey opens the confirmation for deleting one key.
func (a *App) askDeleteKey(s *session, key string) {
	if !s.writable() {
		return
	}
	if s == nil || key == "" {
		return
	}
	a.rowConfirmOpen = false
	a.pending = pendingDelete{sessionID: s.id, path: key}
	a.confirmPhrase = ""
	a.confirmOpen = true
}

// askDeleteNamespace opens the confirmation for deleting every key under
// a namespace.
func (a *App) askDeleteNamespace(s *session, path string) {
	if !s.writable() {
		return
	}
	if s == nil || path == "" {
		return
	}
	a.rowConfirmOpen = false
	a.pending = pendingDelete{sessionID: s.id, path: path, namespace: true}
	a.confirmPhrase = ""
	a.confirmOpen = true
}

// askDeleteKeys opens the confirmation for deleting checked keys.
func (a *App) askDeleteKeys(s *session, keys []string) {
	if !s.writable() {
		return
	}
	if s == nil || len(keys) == 0 {
		return
	}
	a.rowConfirmOpen = false
	a.pending = pendingDelete{sessionID: s.id, keys: keys}
	a.confirmPhrase = ""
	a.confirmOpen = true
}

// askFlush opens the confirmation for emptying the current database.
func (a *App) askFlush(s *session) {
	if !s.writable() {
		return
	}
	if s == nil {
		return
	}
	a.rowConfirmOpen = false
	a.pending = pendingDelete{sessionID: s.id, flush: true}
	a.confirmPhrase = ""
	a.confirmOpen = true
}

// askSlowReset opens the confirmation for emptying the server's slow log.
func (a *App) askSlowReset(s *session) {
	if !s.writable() {
		return
	}
	a.confirmOpen, a.rowConfirmOpen = false, false
	a.slowResetID = s.id
	a.slowResetOpen = true
}

// openRename edits a key's name.
func (a *App) openRename(s *session, key string) {
	a.protectEdits(s, func() { a.openRenameNow(s, key) })
}

func (a *App) openRenameNow(s *session, key string) {
	if !s.writable() {
		return
	}
	if s == nil || key == "" {
		return
	}
	if !utf8.ValidString(key) {
		s.setStatusErr(localError("binary key names cannot be renamed"))
		return
	}
	a.modalSessionID = s.id
	a.renameOpen, a.ttlOpen, a.newKeyOpen, a.rowOpen = true, false, false, false
	a.renameFrom, a.renameTo, a.renameErr, a.renameBusy = key, key, "", false
}

// openTTL edits the expiration of one or more keys, in seconds.
func (a *App) openTTL(s *session, keys []string) {
	if !s.writable() {
		return
	}
	if s == nil || len(keys) == 0 {
		return
	}
	a.modalSessionID = s.id
	a.renameOpen, a.ttlOpen, a.newKeyOpen, a.rowOpen = false, true, false, false
	a.ttlKeys = append([]string(nil), keys...)
	a.ttlErr, a.ttlBusy = "", false
	a.ttlSeconds = 3600
	if len(keys) == 1 && keys[0] == s.valKey {
		if s.val.TTL > 0 {
			a.ttlSeconds = float64(int64(s.val.TTL / time.Second))
		} else {
			a.ttlSeconds = 0
		}
	}
}

// openNewKey starts a key of one of the basic types.
func (a *App) openNewKey(s *session) {
	a.protectEdits(s, func() { a.openNewKeyNow(s) })
}

func (a *App) openNewKeyNow(s *session) {
	if !s.writable() {
		return
	}
	if s == nil {
		return
	}
	a.modalSessionID = s.id
	a.renameOpen, a.ttlOpen, a.newKeyOpen, a.rowOpen = false, false, true, false
	a.newKeyName, a.newKeyType, a.newKeyValue = "", "string", ""
	a.newKeyTTL, a.newKeyErr, a.newKeyBusy = 0, "", false
}

// isCollection reports whether typ is one of the editable collections.
func isCollection(typ string) bool {
	switch typ {
	case "hash", "list", "set", "zset":
		return true
	}
	return false
}

func canAddRow(typ string) bool {
	return isCollection(typ) || typ == "stream"
}

// openRowAdd opens the dialog for a new collection row.
func (a *App) openRowAdd(s *session) {
	if !s.writable() {
		return
	}
	if s == nil || s.valKey == "" || !canAddRow(s.val.Type) {
		return
	}
	a.modalSessionID, a.rowSessionID = s.id, s.id
	a.renameOpen, a.ttlOpen, a.newKeyOpen, a.rowConfirmOpen = false, false, false, false
	a.rowOpen = true
	a.rowPinned = false
	a.rowGeneration++
	a.row = rowEdit{key: s.valKey, typ: s.val.Type, mode: "add", index: len(s.val.Items), score: "0", codec: s.valueCodec()}
}

// rowAt reads the row the value panel shows at index into an edit.
func (a *App) rowAt(s *session, row int) rowEdit {
	if s == nil {
		return rowEdit{}
	}
	r := rowEdit{key: s.valKey, typ: s.val.Type, mode: "edit", index: row}
	switch s.val.Type {
	case "hash":
		if row < 0 || row >= len(s.val.Pairs) {
			return rowEdit{}
		}
		r.field, r.oldField, r.value = s.val.Pairs[row][0], s.val.Pairs[row][0], s.val.Pairs[row][1]
	case "list":
		if row < 0 || row >= len(s.val.Items) {
			return rowEdit{}
		}
		r.value = s.val.Items[row]
		if row < len(s.valIndices) {
			r.index = s.valIndices[row]
		}
	case "set":
		if row < 0 || row >= len(s.val.Items) {
			return rowEdit{}
		}
		r.member, r.oldMember = s.val.Items[row], s.val.Items[row]
	case "zset":
		if row < 0 || row >= len(s.val.Pairs) {
			return rowEdit{}
		}
		r.member, r.oldMember, r.score = s.val.Pairs[row][0], s.val.Pairs[row][0], s.val.Pairs[row][1]
	case "stream":
		idx := row
		if len(s.streamIndices) > 0 {
			if row < 0 || row >= len(s.streamIndices) {
				return rowEdit{}
			}
			idx = s.streamIndices[row]
		}
		if idx < 0 || idx >= len(s.val.Stream) {
			return rowEdit{}
		}
		r.index = idx
		r.field, r.streamID = s.val.Stream[idx].ID, s.val.Stream[idx].ID
	default:
		return rowEdit{}
	}
	return r
}

// openRowEdit opens the dialog for an existing collection row.
func (a *App) openRowEdit(s *session, row int) {
	if !s.writable() {
		return
	}
	r := a.rowAt(s, row)
	if r.typ == "" {
		return
	}
	a.modalSessionID, a.rowSessionID = s.id, s.id
	a.renameOpen, a.ttlOpen, a.newKeyOpen, a.rowConfirmOpen = false, false, false, false
	a.rowOpen = true
	a.rowPinned = false
	a.row = r
	a.prepareRowValue(s)
}

// askDeleteRow opens the confirmation for one collection row.
func (a *App) askDeleteRow(s *session, row int) {
	if !s.writable() {
		return
	}
	r := a.rowAt(s, row)
	if r.typ == "" {
		return
	}
	a.confirmOpen = false
	a.rowSessionID = s.id
	a.rowConfirm = r
	a.rowConfirmOpen = true
}

func (a *App) rowDeleteTitle(r rowEdit) (string, string) {
	var title string
	switch r.typ {
	case "hash":
		title = a.tr("Delete field “%s”?", r.field)
	case "list":
		title = a.tr("Delete element #%d?", r.index)
	case "set":
		title = a.tr("Delete member “%s”?", r.member)
	case "zset":
		title = a.tr("Delete member “%s”?", r.member)
	case "stream":
		title = a.tr("Delete entry %s?", r.streamID)
	default:
		title = a.tr("Delete row?")
	}
	return title, a.tr("This cannot be undone.")
}

// sessionMenu offers the connection's less frequent actions.
func (a *App) sessionMenu(c *ui.Context, pal *palette, s *session) {
	ui.Column(c).Width(198).Padding(4).Children(func() {
		menuItem(c, pal, a.tr("New key…"), iconPlus, false, func() {
			a.sessionMenuOpen = false
			a.openNewKey(s)
		}).Disabled(s.cfg.ReadOnly)
		keys := s.selectedKeys()
		if len(keys) == 0 && s.valKey != "" {
			keys = []string{s.valKey}
		}
		menuItem(c, pal, a.tr("Set TTL…"), iconClock, false, func() {
			a.sessionMenuOpen = false
			a.openTTL(s, keys)
		}).Disabled(s.cfg.ReadOnly)
		autoLabel := "Auto refresh (5s)"
		if s.autoRefresh {
			autoLabel = "Stop auto refresh"
		}
		menuItem(c, pal, a.tr(autoLabel), iconRefresh, false, func() {
			a.sessionMenuOpen = false
			s.toggleAutoRefresh()
		})
		checkLabel := "Check mode"
		if s.checkMode {
			checkLabel = "Exit check mode"
		}
		menuItem(c, pal, a.tr(checkLabel), iconCheck, false, func() {
			a.sessionMenuOpen = false
			s.toggleCheckMode()
		})
		label := "Export loaded keys…"
		if len(s.selectedKeys()) > 0 {
			label = "Export selected keys…"
		}
		menuItem(c, pal, a.tr(label), iconSave, false, func() {
			a.sessionMenuOpen = false
			a.exportSessionCSV(s)
		})
		menuItem(c, pal, a.tr("Import CSV…"), iconFolder, false, func() {
			a.sessionMenuOpen = false
			a.importSessionCSV(s)
		}).Disabled(s.cfg.ReadOnly)
		ui.Box(c).Height(1).Background(pal.divider).Margin(4, 4)
		menuItem(c, pal, a.tr("Flush database…"), iconTrash, true, func() {
			a.sessionMenuOpen = false
			a.askFlush(s)
		}).Disabled(s.cfg.ReadOnly)
		menuItem(c, pal, a.tr("Disconnect"), iconUnplug, false, func() {
			a.sessionMenuOpen = false
			a.closeSession(a.active)
		})
	})
}

func (a *App) copy(c *ui.Context, s *session, text string) {
	c.Services().WriteClipboard(text)
	if s != nil {
		s.setStatus("Copied %s", text)
	} else {
		a.setStatus("Copied %s", text)
	}
}

func (a *App) setStatus(key string, args ...any) {
	a.status, a.statusErr = msg(key, args...), false
}

func (a *App) setStatusErr(err error) {
	if err == nil {
		return
	}
	a.status, a.statusErr = errorMessage(err), true
}

func (a *App) clearStatus() {
	a.status, a.statusErr = message{}, false
}

// viewCardStatus is the footer of the working card: the latest operation's
// message, or a quiet status, with a dot and a way to dismiss an error.
func (a *App) viewCardStatus(c *ui.Context, pal *palette, s *session) {
	stored, isErr := a.status, a.statusErr
	if s != nil {
		stored, isErr = s.status, s.statusErr
	}
	msg := stored.text(a)
	color := pal.muted
	switch {
	case isErr:
		color = pal.danger
	case s != nil && s.editorModified():
		msg, color = a.tr("Unsaved changes"), pal.warning
	case msg == "" && s != nil && s.scanning:
		msg, color = a.tr("Scanning keys…"), pal.warning
	case msg == "" && s != nil:
		msg, color = a.tr("Ready"), pal.success
	case msg == "":
		msg = a.tr("Ready to connect.")
	default:
		color = pal.success
	}
	ui.Row(c).Height(28).Shrink(0).Padding(0, 12).Gap(7).AlignItems(ui.Center).Children(func() {
		statusDot(c, color)
		ui.Text(c, msg).FontSize(11.5).TextColor(color).SingleLine().Grow(1).Tooltip(msg)
		if s != nil && s.tool == "browser" && s.valKey != "" {
			if meta := s.valMeta(); meta != "" {
				ui.Text(c, meta).FontSize(11).TextColor(pal.faint).SingleLine().Tooltip(meta)
			}
		}
		if msg != "" && ((s != nil && s.status.key != "") || (s == nil && a.status.key != "")) {
			iconButton(c, "status-dismiss", iconClose, a.tr("Dismiss"), pal, func() {
				if s != nil {
					s.clearStatus()
				} else {
					a.clearStatus()
				}
			})
		}
	})
}

// viewDialogs draws the confirmation of a pending destructive action.
func (a *App) viewDialogs(c *ui.Context) {
	if a.editGuard != nil {
		a.viewEditGuard(c)
		return
	}
	if a.slowResetOpen {
		s := a.sessionByID(a.slowResetID)
		switch ui.AlertDialog(c, &a.slowResetOpen, a.tr("Reset slow log?"), a.slowResetContext(s), a.tr("Cancel"), a.tr("Reset")) {
		case 1:
			if s == nil {
				a.setStatus("That connection is closed.")
			} else {
				s.resetSlowLog()
			}
		}
	} else if a.rowConfirmOpen {
		s := a.sessionByID(a.rowSessionID)
		title, message := a.rowDeleteTitle(a.rowConfirm)
		if s != nil {
			message = s.connectionContext() + "\n\n" + message
		}
		switch ui.AlertDialog(c, &a.rowConfirmOpen, title, message, a.tr("Cancel"), a.tr("Delete")) {
		case 1:
			r := a.rowConfirm
			a.rowConfirm = rowEdit{}
			if s == nil {
				a.setStatus("That connection is closed.")
			} else {
				s.deleteRow(r, func(err error) {
					if err != nil {
						s.setStatusErr(err)
					}
				})
			}
		case 0, ui.AlertDismissed:
			a.rowConfirm = rowEdit{}
		}
	} else if a.confirmOpen {
		a.viewDeleteConfirmation(c)
	}
	a.viewRename(c)
	a.viewTTL(c)
	a.viewNewKey(c)
	a.viewRowDialog(c)
	a.viewGroupRename(c)
	a.viewConnDelete(c)
	a.viewCSVImport(c)
	a.viewCSVExport(c)
	a.viewGroupDestroy(c)
	a.viewPalette(c)
}

func (a *App) viewRename(c *ui.Context) {
	if !a.renameOpen {
		return
	}
	pal := a.paletteFor(c)
	s := a.sessionByID(a.modalSessionID)
	if s == nil || s.closed {
		a.renameOpen = false
		return
	}
	submit := func() {
		name := strings.TrimSpace(a.renameTo)
		if name == "" {
			a.renameErr = "A name is required."
			return
		}
		if name == a.renameFrom {
			a.renameOpen = false
			return
		}
		a.renameBusy = true
		a.renameErr = ""
		s.renameKey(a.renameFrom, name, func(err error) {
			a.renameBusy = false
			if err != nil {
				a.renameErr = err.Error()
				return
			}
			a.renameOpen = false
		})
	}
	ui.Modal(c.Key("rename-modal"), &a.renameOpen, func() {
		ui.Column(c).Width(380).Padding(4).Gap(10).Children(func() {
			ui.Text(c, a.tr("Rename key")).FontSize(15).FontWeight(650).TextColor(pal.text)
			ui.Text(c, a.renameFrom).FontSize(11).TextColor(pal.faint).SingleLine().Ellipsis("…")
			ui.Field(c, a.tr("New name"), func() {
				if ui.TextInput(c, &a.renameTo).Width(340).Submitted() {
					submit()
				}
			})
			if a.renameErr != "" {
				ui.Text(c, a.tr(a.renameErr)).FontSize(11.5).TextColor(pal.danger)
			}
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if textButton(c, "rename-cancel", a.tr("Cancel"), false, false, pal).Clicked() {
					a.renameOpen = false
				}
				label := a.tr("Rename")
				if a.renameBusy {
					label = a.tr("Renaming…")
				}
				if textButton(c, "rename-ok", label, true, false, pal).Disabled(a.renameBusy).Clicked() {
					submit()
				}
			})
		})
	})
}

func (a *App) viewTTL(c *ui.Context) {
	if !a.ttlOpen {
		return
	}
	pal := a.paletteFor(c)
	s := a.sessionByID(a.modalSessionID)
	if s == nil || s.closed {
		a.ttlOpen = false
		return
	}
	submit := func() {
		a.ttlBusy = true
		a.ttlErr = ""
		s.setTTL(a.ttlKeys, int64(a.ttlSeconds), func(err error) {
			a.ttlBusy = false
			if err != nil {
				a.ttlErr = err.Error()
				return
			}
			a.ttlOpen = false
		})
	}
	ui.Modal(c.Key("ttl-modal"), &a.ttlOpen, func() {
		ui.Column(c).Width(380).Padding(4).Gap(10).Children(func() {
			ui.Text(c, a.tr("Set TTL")).FontSize(15).FontWeight(650).TextColor(pal.text)
			ui.Text(c, a.tr("%d key(s)", len(a.ttlKeys))).FontSize(11).TextColor(pal.faint).SingleLine()
			ui.Field(c, a.tr("Expire after (seconds)"), func() {
				ui.NumberInput(c, &a.ttlSeconds, 0, 10*365*24*3600, 60).Width(180)
			})
			ui.Text(c, a.tr("0 removes the expiration.")).FontSize(11).TextColor(pal.faint)
			if a.ttlErr != "" {
				ui.Text(c, a.tr(a.ttlErr)).FontSize(11.5).TextColor(pal.danger)
			}
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if textButton(c, "ttl-cancel", a.tr("Cancel"), false, false, pal).Clicked() {
					a.ttlOpen = false
				}
				label := a.tr("Set TTL")
				if a.ttlBusy {
					label = a.tr("Saving…")
				}
				if textButton(c, "ttl-ok", label, true, false, pal).Disabled(a.ttlBusy).Clicked() {
					submit()
				}
			})
		})
	})
}

func (a *App) viewNewKey(c *ui.Context) {
	if !a.newKeyOpen {
		return
	}
	pal := a.paletteFor(c)
	s := a.sessionByID(a.modalSessionID)
	if s == nil || s.closed {
		a.newKeyOpen = false
		return
	}
	types := []string{"string", "json", "hash", "list", "set", "zset"}
	submit := func() {
		name := strings.TrimSpace(a.newKeyName)
		if name == "" {
			a.newKeyErr = "A key name is required."
			return
		}
		a.newKeyBusy = true
		a.newKeyErr = ""
		s.createKey(name, a.newKeyType, a.newKeyValue, int64(a.newKeyTTL), func(err error) {
			a.newKeyBusy = false
			if err != nil {
				a.newKeyErr = err.Error()
				return
			}
			a.newKeyOpen = false
		})
	}
	ui.Modal(c.Key("new-key-modal"), &a.newKeyOpen, func() {
		ui.Column(c).Width(430).Padding(4).Gap(10).Children(func() {
			ui.Text(c, a.tr("New key")).FontSize(15).FontWeight(650).TextColor(pal.text)
			ui.Field(c, a.tr("Key"), func() {
				ui.TextInput(c, &a.newKeyName).Placeholder("user:1").Width(390)
			})
			ui.Row(c).Gap(10).Children(func() {
				ui.Field(c, a.tr("Type"), func() {
					ui.Select(c, &a.newKeyType, types).Width(140)
				})
				ui.Field(c, a.tr("TTL (seconds)"), func() {
					ui.NumberInput(c, &a.newKeyTTL, 0, 10*365*24*3600, 60).Width(150)
				})
			})
			ui.Field(c, a.tr("Value"), func() {
				ui.TextArea(c, &a.newKeyValue).Width(390).Height(140).
					Label(a.tr("The key's value")).Placeholder(a.tr("The key's value"))
			})
			ui.Text(c, a.tr(newKeyHelp(a.newKeyType))).FontSize(11).TextColor(pal.faint)
			if a.newKeyErr != "" {
				ui.Text(c, a.tr(a.newKeyErr)).FontSize(11.5).TextColor(pal.danger)
			}
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if textButton(c, "new-key-cancel", a.tr("Cancel"), false, false, pal).Clicked() {
					a.newKeyOpen = false
				}
				label := a.tr("Create")
				if a.newKeyBusy {
					label = "Creating…"
				}
				if textButton(c, "new-key-ok", label, true, false, pal).Disabled(a.newKeyBusy).Clicked() {
					submit()
				}
			})
		})
	})
}

func (a *App) viewRowDialog(c *ui.Context) {
	if !a.rowOpen {
		return
	}
	pal := a.paletteFor(c)
	s := a.sessionByID(a.rowSessionID)
	if s == nil || s.closed {
		a.rowOpen = false
		return
	}
	r := a.row
	title := "Edit row"
	switch r.typ {
	case "hash":
		if r.mode == "add" {
			title = a.tr("Add field")
		} else {
			title = a.tr("Edit field")
		}
	case "list":
		if r.mode == "add" {
			title = a.tr("Add element")
		} else {
			title = a.tr("Edit element #%d", r.index)
		}
	case "set":
		if r.mode == "add" {
			title = a.tr("Add member")
		} else {
			title = a.tr("Edit member")
		}
	case "zset":
		if r.mode == "add" {
			title = a.tr("Add member")
		} else {
			title = a.tr("Edit member")
		}
	case "stream":
		title = a.tr("Add entry")
	}
	submit := func() {
		edit := a.row
		if !edit.bulk && edit.typ == "hash" && strings.TrimSpace(edit.field) == "" {
			a.row.err = "A field name is required."
			return
		}
		if !edit.bulk && (edit.typ == "set" || edit.typ == "zset") && strings.TrimSpace(edit.member) == "" {
			a.row.err = "A member is required."
			return
		}
		if edit.typ == "stream" {
			if _, err := parseStreamFields(edit.value); err != nil {
				a.row.err = err.Error()
				return
			}
		}
		a.row.busy = true
		a.row.err = ""
		generation := a.rowGeneration
		s.saveRow(edit, func(err error) {
			if !a.rowOpen || a.rowGeneration != generation || a.rowSessionID != s.id {
				return
			}
			a.row.busy = false
			if err != nil {
				a.row.err = err.Error()
				return
			}
			if a.rowPinned && edit.mode == "edit" {
				s.setStatus("Saved; editor pinned")
				return
			}
			a.rowOpen = false
		})
	}
	ui.Modal(c.Key("row-modal"), &a.rowOpen, func() {
		ui.Column(c).Width(400).Padding(4).Gap(10).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(15).FontWeight(650).TextColor(pal.text).Grow(1)
				if r.mode == "edit" {
					label := "Pin editor"
					if a.rowPinned {
						label = "Unpin editor"
					}
					pin := ui.ButtonBase(c.Key("row-pin")).Size(28, 26).Radius(6).Center().
						Label(a.tr(label)).Tooltip(a.tr(label)).Border(0, ui.Transparent)
					if a.rowPinned {
						pin.Background(pal.accent.Alpha(0.14)).TextColor(pal.accent)
					} else {
						pin.TextColor(pal.faint)
					}
					pin.Children(func() {
						ui.Icon(c, iconPin).Size(14, 14)
					})
					if pin.Clicked() {
						a.rowPinned = !a.rowPinned
					}
				}
			})
			if r.mode == "add" && r.typ != "stream" {
				ui.Checkbox(c, &a.row.bulk, a.tr("Bulk add"))
				if r.typ == "list" {
					ui.Checkbox(c, &a.row.head, a.tr("Prepend to list"))
				}
			}
			if a.row.bulk {
				ui.TextArea(c, &a.row.bulkText).Width(360).Height(150).Placeholder(a.tr(newKeyHelp(r.typ)))
				ui.Text(c, a.tr(newKeyHelp(r.typ))).FontSize(11).TextColor(pal.faint)
			} else {
				switch r.typ {
				case "hash":
					ui.Field(c, a.tr("Field"), func() {
						ui.TextInput(c, &a.row.field).Width(360)
					})
					ui.Field(c, a.tr("Value"), func() {
						ui.TextArea(c, &a.row.value).ReadOnly(a.row.loading).Width(360).Height(90)
					})
				case "list":
					ui.Field(c, a.tr("Value"), func() {
						ui.TextArea(c, &a.row.value).ReadOnly(a.row.loading).Width(360).Height(90)
					})
				case "set":
					ui.Field(c, a.tr("Member"), func() {
						ui.TextInput(c, &a.row.member).ReadOnly(a.row.loading).Width(360)
					})
				case "zset":
					ui.Field(c, a.tr("Member"), func() {
						ui.TextInput(c, &a.row.member).ReadOnly(a.row.loading).Width(360)
					})
					ui.Field(c, a.tr("Score"), func() {
						ui.TextInput(c, &a.row.score).Width(140)
					})
				case "stream":
					ui.Field(c, a.tr("Entry ID"), func() {
						ui.TextInput(c, &a.row.streamID).Placeholder(a.tr("* for auto")).Width(200)
					})
					ui.Field(c, a.tr("Fields"), func() {
						ui.TextArea(c, &a.row.value).Width(360).Height(100).Placeholder("field=value")
					})
					ui.Text(c, a.tr("One field=value per line.")).FontSize(11).TextColor(pal.faint)
				}
			}
			if a.row.loading && a.row.err == "" {
				ui.Text(c, a.tr("Decoding…")).FontSize(11).TextColor(pal.muted)
			}
			if a.row.err != "" {
				ui.Text(c, a.tr(a.row.err)).FontSize(11.5).TextColor(pal.danger)
			}
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if textButton(c, "row-cancel", a.tr("Cancel"), false, false, pal).Clicked() {
					a.rowOpen = false
				}
				label := a.tr("Save")
				if a.row.busy {
					label = a.tr("Saving…")
				}
				if textButton(c, "row-ok", label, true, false, pal).Disabled(a.row.busy || a.row.loading).Clicked() {
					submit()
				}
			})
		})
	})
}

func newKeyHelp(typ string) string {
	switch typ {
	case "hash":
		return "One field=value per line."
	case "list", "set":
		return "One element per line."
	case "zset":
		return "One member=score per line."
	case "json":
		return "Valid JSON; RedisJSON must be loaded."
	default:
		return "The string's value."
	}
}

// sniffLanguage picks an editor language for a string value.
func sniffLanguage(s string) *editor.Language {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		if l := editor.LanguageNamed("JSON"); l != nil {
			return l
		}
	}
	if strings.HasPrefix(t, "<") {
		if l := editor.LanguageNamed("XML"); l != nil {
			return l
		}
	}
	return nil
}

func formatTTL(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}

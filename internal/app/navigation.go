package app

import (
	"fmt"
	"time"
)

// Recent keys stay in memory, contain no values, and have a small fixed bound.
type recentKey struct {
	profileID string
	db        int
	key       string
}

func (a *App) rememberKey(s *session) {
	if len(s.valKey) > 4096 || s.valKey == "" {
		return
	}
	r := recentKey{s.id, s.db, s.valKey}
	next := []recentKey{r}
	for _, old := range a.recentKeys {
		if old != r && len(next) < 20 {
			next = append(next, old)
		}
	}
	a.recentKeys = next
}

func (a *App) openProfile(id string, after func(*session)) {
	if s := a.sessionByID(id); s != nil {
		a.active = a.sessionIndex(id)
		a.stopOtherLive(a.active)
		if after != nil {
			after(s)
		}
		return
	}
	p, ok := a.findProfile(id)
	if !ok || a.connConnecting {
		return
	}
	a.active = -1
	a.stopOtherLive(-1)
	a.selProfile = p.ID
	a.fillConnForm(p)
	a.afterConnect = after
	a.connectSelected()
}

func (a *App) openRecentKey(r recentKey) {
	a.openProfile(r.profileID, func(s *session) {
		show := func() {
			s.setTool("browser")
			// Finish or invalidate pending scans before revealing an explicitly opened key.
			s.cancelScan()
			s.scanID++
			s.scanning = false
			s.keys.add(r.key)
			s.reveal(r.key)
			s.sel.Clear()
			s.sel.Add(r.key)
			s.loadValue(r.key)
		}
		a.protectEdits(s, func() {
			if s.db == r.db {
				show()
				return
			}
			s.switchDB(r.db, func(err error) {
				if err == nil {
					show()
				}
			})
		})
	})
}

func (a *App) navigationCommands() []paletteCommand {
	var cmds []paletteCommand
	for _, p := range a.profiles {
		cmds = append(cmds, paletteCommand{"connect-" + p.ID, a.tr("Open connection: %s", p.label()), p.summary(), "", func() { a.openProfile(p.ID, nil) }})
	}
	for i, r := range a.recentKeys {
		p, ok := a.findProfile(r.profileID)
		if !ok {
			continue
		}
		cmds = append(cmds, paletteCommand{fmt.Sprintf("recent-%d", i), a.tr("Recent key: %s", displayKey(r.key)), p.label() + " · " + p.dbLabel(r.db), "", func() { a.openRecentKey(r) }})
	}
	return cmds
}

func (a *App) schedulePrefsSave() {
	if a.prefsSaveTimer != nil {
		a.prefsSaveTimer.Stop()
	}
	a.prefsSaveTimer = time.AfterFunc(350*time.Millisecond, func() {
		a.update(func() {
			if !a.closed {
				a.prefsSaveTimer = nil
				a.savePrefs()
			}
		})
	})
}

func (a *App) toggleSidebar() {
	a.prefs.SidebarHidden = !a.prefs.SidebarHidden
	a.savePrefs()
}

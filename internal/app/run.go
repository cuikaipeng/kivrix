package app

// Portions of the desktop layout and controls are adapted from kero.
// Copyright (c) 2026 EGOIST. MIT License; see third_party/licenses/kero.txt.

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/locales/zh-Hans"
	_ "github.com/egoist/mygo/locales/zh-Hant"
	"github.com/egoist/mygo/ui"
)

func Main() {
	http.DefaultClient = updateHTTPClient(http.DefaultClient)
	a := newApp()
	mygo.App.OnLocaleChanged(func(string) {
		a.update(func() {
			mygo.App.SetMenu(a.applicationMenu())
			if a.settingsWin != nil {
				a.settingsWin.SetTitle(a.tr("Settings"))
			}
		})
	})
	mygo.App.WhenReady(func() {
		if dir, err := mygo.App.Path(mygo.PathUserData); err == nil {
			if err := migrateLegacyUserData(dir); err != nil {
				log.Printf("kivrix: migrate old data: %v", err)
			}
			a.profilesPath = filepath.Join(dir, "profiles.json")
			a.prefsPath = filepath.Join(dir, "preferences.json")
			if prefs, err := loadPreferences(a.prefsPath); err == nil {
				a.prefs = prefs
			} else {
				a.setStatusErr(localError("could not read preferences.json: %s", err))
			}
			a.initSecretStore(dir)
			ps, err := loadProfiles(a.profilesPath)
			switch {
			case err == nil:
				a.profiles = ps
			default:
				// Keep an unreadable file instead of overwriting it on the
				// first save: move it aside and say where it went.
				backup := a.profilesPath + ".bak"
				if rerr := os.Rename(a.profilesPath, backup); rerr == nil {
					a.setStatusErr(localError("could not read profiles.json (%s); moved it to %s", err, backup))
				} else {
					a.setStatusErr(localError("could not read profiles.json: %s", err))
				}
				log.Printf("kivrix: read profiles: %v", err)
			}
		} else {
			a.setStatusErr(localError("could not find the app data directory: %s", err))
		}
		a.applyTheme()
		a.win = mygo.NewWindow(framed(mygo.WindowOptions{
			Title:     "Kivrix",
			Width:     1180,
			Height:    760,
			MinWidth:  760,
			MinHeight: 520,
			// Opens where the user left it last time.
			StateKey:        "main",
			Vibrancy:        a.windowVibrancy(),
			BackgroundColor: "light-dark(#f3eef6, #17141f)",
			Content:         ui.View(a.view),
		}))
		a.win.OnClose(func(e *mygo.CloseEvent) {
			if a.allowClose {
				return
			}
			for _, s := range a.sessions {
				if s.hasEdits() {
					e.PreventDefault()
					a.protectAllEdits(func() { a.allowClose = true; a.win.Close() })
					a.win.Invalidate()
					return
				}
			}
		})
		a.win.OnClosed(func() {
			if a.prefsSaveTimer != nil {
				a.prefsSaveTimer.Stop()
				a.prefsSaveTimer = nil
				a.savePrefs()
			}
			a.closed = true
			if a.updateCancel != nil {
				a.updateCancel()
			}
			if a.rowCancel != nil {
				a.rowCancel()
			}
			for _, s := range a.sessions {
				s.close()
			}
			a.sessions = nil
			a.secretCache = nil
			a.form = connForm{}
		})
		a.win.OnMinimize(func() {
			a.suspended = true
			for _, s := range a.sessions {
				s.stopLive()
				s.stopStatusSampling()
			}
		})
		a.win.OnRestore(func() { a.suspended = false })
		a.applyLocale()
		a.restoreLastSession()
		a.checkUpdatesOnLaunch()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// framed hides the title bar and keeps the window's controls where the
// platform has them, so the view draws the title bar as Kero does.
func framed(o mygo.WindowOptions) mygo.WindowOptions {
	if runtime.GOOS == "darwin" {
		o.TitleBarStyle = mygo.TitleBarHiddenInset
		o.TitleBarHeight = topBarH
		// Center the traffic lights on the bar's midline, as Kero does.
		o.TrafficLightPosition = &mygo.Point{X: 18, Y: topBarH/2 - 7}
		return o
	}
	o.TitleBarStyle = mygo.TitleBarHidden
	o.TitleBarHeight = topBarH
	o.HideWindowButtons = true
	o.AutoHideMenuBar = true
	return o
}

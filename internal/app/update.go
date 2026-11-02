package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const appVersion = "0.1.0"

// MyGo's updater uses the default client. Preserve its transport settings but
// reject redirects that would downgrade a signed update download to plain HTTP.
func updateHTTPClient(base *http.Client) *http.Client {
	client := *base
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return fmt.Errorf("update redirect is not HTTPS")
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many update redirects")
		}
		if base.CheckRedirect != nil {
			return base.CheckRedirect(req, via)
		}
		return nil
	}
	return &client
}

// MyGo uses the release feed and public key embedded by the packager. Like
// Kero, we prepare a signed update in place, then let the user restart into it.
type appUpdate struct {
	version string
	prepare func(context.Context, func(int64, int64)) error
}

// Native boundaries are replaceable so flow tests never update the test runner.
var (
	findAppUpdate = func(ctx context.Context) (*appUpdate, error) {
		up, err := mygo.Updater.Check(ctx)
		if err != nil || up == nil {
			return nil, err
		}
		return &appUpdate{version: up.Version, prepare: up.Install}, nil
	}
	restartApp = mygo.App.Relaunch
)

func (a *App) checkUpdatesOnLaunch() {
	if a.prefs.CheckUpdates {
		a.beginUpdateCheck(false)
	}
}

func (a *App) checkUpdates() { a.beginUpdateCheck(true) }

func (a *App) beginUpdateCheck(manual bool) {
	if a.closed {
		return
	}
	a.updateNotice = a.updateNotice || manual
	if a.updateChecking || a.updateDownloading || a.updateReady {
		a.refreshUpdateViews()
		return
	}
	a.updateChecking = true
	a.updateStatus = msg("Checking…")
	a.updateErr = ""
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	a.updateCancel = cancel
	go func() {
		defer cancel()
		up, err := findAppUpdate(ctx)
		a.update(func() {
			if a.closed {
				return
			}
			a.updateChecking, a.updateCancel = false, nil
			if errors.Is(err, mygo.ErrUpdatesDisabled) {
				a.updateStatus = msg("This installation cannot update itself. Use an installed release in a writable location.")
			} else if err != nil {
				a.updateErr = err.Error()
				a.updateStatus = msg("Update check failed: %s", err)
			} else {
				a.availableUpdate = up
				if up == nil {
					a.updateStatus = msg("Up to date")
				} else {
					a.updateStatus = msg("Version %s is available", up.version)
				}
			}
			a.refreshUpdateViews()
		})
	}()
	a.refreshUpdateViews()
}

// Both windows show the same state. Window.Update invalidates only the main
// window, so the separate Settings window also needs to be redrawn.
func (a *App) refreshUpdateViews() {
	if a.win != nil {
		a.win.Invalidate()
	}
	if a.settingsWin != nil {
		a.settingsWin.Invalidate()
	}
}

func (a *App) downloadUpdate() {
	if a.closed || a.updateChecking || a.updateDownloading || a.updateReady || a.availableUpdate == nil {
		return
	}
	a.updateDownloading, a.updateNotice = true, true
	a.updateErr = ""
	a.updateBytes, a.updateTotal = 0, 0
	a.updateStatus = msg("Downloading and preparing update…")
	up := a.availableUpdate
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	a.updateCancel = cancel
	// At most one progress callback is queued, even while the UI is busy.
	var downloaded, total atomic.Int64
	var queued atomic.Bool
	progress := func(n, size int64) {
		downloaded.Store(n)
		total.Store(size)
		if queued.CompareAndSwap(false, true) {
			a.update(func() {
				queued.Store(false)
				if !a.closed && a.updateDownloading {
					a.updateBytes, a.updateTotal = downloaded.Load(), total.Load()
					a.refreshUpdateViews()
				}
			})
		}
	}
	go func() {
		defer cancel()
		err := up.prepare(ctx, progress)
		a.update(func() {
			if a.closed {
				return
			}
			a.updateDownloading, a.updateCancel = false, nil
			if err != nil {
				a.updateErr = err.Error()
				a.updateStatus = msg("Update download failed: %s", err)
			} else {
				a.updateReady = true
				a.updateStatus = msg("Update ready. Install and restart when you are ready.")
			}
			a.refreshUpdateViews()
		})
	}()
	a.refreshUpdateViews()
}

func (a *App) installUpdate() {
	if !a.updateReady || a.closed {
		return
	}
	// The edit confirmation belongs to the main window, even when installation
	// was requested from Settings.
	if a.win != nil {
		a.win.Show()
		a.win.Focus()
	}
	a.protectAllEdits(func() {
		a.savePrefs()
		restartApp()
	})
	a.refreshUpdateViews()
}

func (a *App) updateAction() (string, func()) {
	switch {
	case a.updateDownloading:
		if a.updateTotal > 0 {
			percent := int(min(a.updateBytes, a.updateTotal) * 100 / a.updateTotal)
			return a.tr("Downloading… %d%%", percent), nil
		}
		return a.tr("Downloading…"), nil
	case a.updateReady:
		return a.tr("Install and restart"), a.installUpdate
	case a.availableUpdate != nil:
		if a.updateStatus.key == "Update download failed: %s" {
			return a.tr("Retry download"), a.downloadUpdate
		}
		return a.tr("Download update"), a.downloadUpdate
	}
	return "", nil
}

func (a *App) viewUpdateNotice(c *ui.Context, pal *palette) {
	label, action := a.updateAction()
	if label == "" {
		if !a.updateNotice {
			return
		}
		label, action = a.tr("Check for updates"), a.checkUpdates
		switch {
		case a.updateChecking:
			label, action = a.tr("Checking…"), nil
		case a.updateErr != "":
			label = a.tr("Update check failed")
		case a.updateStatus.key == "This installation cannot update itself. Use an installed release in a writable location.":
			label, action = a.tr("Updates unavailable"), nil
		case a.updateStatus.key == "Up to date":
			label = a.tr("Up to date")
		}
	}
	failed := a.updateErr != ""
	textButton(c, "top-update", label, !failed && (a.availableUpdate != nil || a.updateReady), failed, pal).
		Shrink(0).Tooltip(a.updateStatus.text(a)).Disabled(action == nil).OnClick(action)
}

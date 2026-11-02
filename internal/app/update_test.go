package app

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func mockUpdater(t *testing.T, find func(context.Context) (*appUpdate, error)) *atomic.Int32 {
	t.Helper()
	oldFind, oldRestart := findAppUpdate, restartApp
	var restarts atomic.Int32
	findAppUpdate, restartApp = find, func() { restarts.Add(1) }
	t.Cleanup(func() { findAppUpdate, restartApp = oldFind, oldRestart })
	return &restarts
}

func TestUpdatePreferenceDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	for _, raw := range []string{"", `{}`, `{"checkUpdates":false}`, `{"checkUpdates":true}`} {
		if raw != "" {
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
		}
		prefs, err := loadPreferences(path)
		if err != nil || prefs.CheckUpdates != (raw != `{"checkUpdates":false}`) {
			t.Fatalf("%s: CheckUpdates=%v, error=%v", raw, prefs.CheckUpdates, err)
		}
	}
}

func TestUpdateLaunchAndManualEntrypoints(t *testing.T) {
	for _, source := range []string{"launch", "disabled", "settings", "menu", "sidebar"} {
		t.Run(source, func(t *testing.T) {
			var calls atomic.Int32
			mockUpdater(t, func(context.Context) (*appUpdate, error) {
				calls.Add(1)
				return nil, nil
			})
			h := newUIFlow(t)
			h.a.prefs.CheckUpdates = source == "launch"
			switch source {
			case "launch", "disabled":
				h.a.checkUpdatesOnLaunch()
			case "settings":
				h.a.prefsOpen = true
				h.ui = ui.NewTester(h.a.settingsView, 820, 620)
				h.click("Check for updates")
			case "menu":
				item := h.a.applicationMenu().ItemByID("check-updates")
				if item == nil {
					t.Fatal("missing application menu item")
				}
				item.Click(item, nil)
			case "sidebar":
				h.click("Connection actions")
				h.click("Check for updates")
			}
			if source == "disabled" {
				if h.a.updateChecking || calls.Load() != 0 {
					t.Fatal("disabled launch check ran")
				}
				return
			}
			h.wait("update check", func() bool { return !h.a.updateChecking })
			if calls.Load() != 1 || h.a.updateStatus.key != "Up to date" {
				t.Fatal("check did not finish")
			}
			if source != "launch" && !h.ui.HasText("Up to date") {
				t.Fatal("manual check had no visible result")
			}
		})
	}
}

func TestUpdateDownloadThenExplicitRestart(t *testing.T) {
	releaseDownload := make(chan struct{})
	progressSent := make(chan struct{})
	var calls atomic.Int32
	up := &appUpdate{version: "0.2.0", prepare: func(ctx context.Context, progress func(int64, int64)) error {
		for i := int64(1); i <= 2000; i++ {
			progress(i, 4000)
		}
		close(progressSent)
		select {
		case <-releaseDownload:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	restarts := mockUpdater(t, func(context.Context) (*appUpdate, error) { calls.Add(1); return up, nil })
	h := newUIFlow(t)
	h.a.checkUpdatesOnLaunch()
	h.wait("new version", func() bool { return !h.a.updateChecking })
	if !h.ui.HasText("Download update") {
		t.Fatal("no home update notice")
	}
	h.click("Download update")
	select {
	case <-progressSent:
	case <-time.After(5 * time.Second):
		t.Fatal("progress reporting blocked the download")
	}
	if len(h.jobs) != 1 {
		t.Fatalf("progress flooded the UI queue: %d callbacks", len(h.jobs))
	}
	h.wait("download progress", func() bool { return h.a.updateBytes == 2000 })
	if !h.ui.HasText("Downloading… 50%") || h.a.updateReady || restarts.Load() != 0 {
		t.Fatal("download prematurely restarted or hid progress")
	}
	h.a.checkUpdates()
	if calls.Load() != 1 || h.a.availableUpdate != up {
		t.Fatal("checking reset an active download")
	}
	close(releaseDownload)
	h.wait("prepared update", func() bool { return h.a.updateReady })
	if restarts.Load() != 0 || !h.ui.HasText("Install and restart") {
		t.Fatal("restart was not explicit")
	}
	h.a.checkUpdates()
	if !h.a.updateReady || calls.Load() != 1 {
		t.Fatal("checking discarded a prepared update")
	}
	h.click("Install and restart")
	if restarts.Load() != 1 {
		t.Fatal("install did not restart")
	}
}

func TestUpdateFailuresAndRetry(t *testing.T) {
	attempt := 0
	up := &appUpdate{version: "0.2.0", prepare: func(context.Context, func(int64, int64)) error {
		attempt++
		if attempt == 1 {
			return errors.New("invalid update signature")
		}
		return nil
	}}
	restarts := mockUpdater(t, func(context.Context) (*appUpdate, error) { return up, nil })
	h := newUIFlow(t)
	h.a.checkUpdates()
	h.wait("new version", func() bool { return !h.a.updateChecking })
	h.click("Download update")
	h.wait("failed download", func() bool { return !h.a.updateDownloading })
	if h.a.updateReady || restarts.Load() != 0 || !strings.Contains(h.a.updateErr, "signature") {
		t.Fatal("failed verification was offered for installation")
	}
	h.click("Retry download")
	h.wait("retry", func() bool { return h.a.updateReady })
}

func TestUpdateCheckFailureAndUnsupportedBuild(t *testing.T) {
	for _, cause := range []error{errors.New("offline"), mygo.ErrUpdatesDisabled} {
		t.Run(cause.Error(), func(t *testing.T) {
			mockUpdater(t, func(context.Context) (*appUpdate, error) { return nil, cause })
			h := newUIFlow(t)
			h.a.checkUpdates()
			h.wait("failed check", func() bool { return !h.a.updateChecking })
			if h.a.updateReady || h.a.availableUpdate != nil || !h.a.updateNotice {
				t.Fatal("failed check was hidden or actionable")
			}
			if !strings.Contains(h.a.updateStatus.text(h.a), "offline") && !strings.Contains(h.a.updateStatus.text(h.a), "writable") {
				t.Fatal(h.a.updateStatus.text(h.a))
			}
			if !h.ui.HasText("Update check failed") && !h.ui.HasText("Updates unavailable") {
				t.Fatal("manual check result was not visible on the home screen")
			}
		})
	}
}

func TestUpdateCloseCancelsWorkAndDropsLateResult(t *testing.T) {
	for _, phase := range []string{"check", "download"} {
		t.Run(phase, func(t *testing.T) {
			finished := make(chan struct{})
			wait := func(ctx context.Context) {
				<-ctx.Done()
				close(finished)
			}
			mockUpdater(t, func(ctx context.Context) (*appUpdate, error) {
				wait(ctx)
				return &appUpdate{version: "0.2.0"}, nil
			})
			h := newUIFlow(t)
			if phase == "check" {
				h.a.checkUpdates()
			} else {
				h.a.availableUpdate = &appUpdate{version: "0.2.0", prepare: func(ctx context.Context, _ func(int64, int64)) error {
					wait(ctx)
					return nil
				}}
				h.a.downloadUpdate()
			}
			h.a.closed = true
			h.a.updateCancel()
			h.nextUpdate()()
			<-finished
			if h.a.updateReady || (phase == "check" && h.a.availableUpdate != nil) {
				t.Fatal("closed application accepted a late update")
			}
		})
	}
}

func TestUpdateNoticeFitsCompactWindows(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		for _, surface := range []string{"home", "settings"} {
			for _, state := range []string{"available", "downloading", "ready"} {
				t.Run(language+"/"+surface+"/"+state, func(t *testing.T) {
					h := newUIFlow(t)
					h.a.prefs.Language = language
					h.a.availableUpdate = &appUpdate{version: "0.2.0"}
					h.a.updateDownloading, h.a.updateReady = state == "downloading", state == "ready"
					h.a.updateBytes, h.a.updateTotal = 42, 100
					h.a.updateStatus = msg("Version %s is available", "0.2.0")
					if h.a.updateReady {
						h.a.updateStatus = msg("Update ready. Install and restart when you are ready.")
					} else if h.a.updateDownloading {
						h.a.updateStatus = msg("Downloading and preparing update…")
					}
					width, height, view := float32(760), float32(520), h.a.view
					if surface == "settings" {
						width, height, view = 700, 480, h.a.settingsView
						h.a.prefsOpen = true
					}
					h.ui = ui.NewTester(view, int(width), int(height))
					h.ui.SetLocale(language)
					label, _ := h.a.updateAction()
					r, ok := h.ui.Find(label)
					if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > width || r.Y+r.H > height {
						t.Fatalf("update action is clipped: %q %+v", label, r)
					}
					if surface == "home" && (r.X < width/2 || r.Y+r.H > topBarH) {
						t.Fatalf("notice is not in the top-right corner: %+v", r)
					}
					saveParityView(t, h.ui, "update-"+language+"-"+surface+"-"+state)
				})
			}
		}
	}
}

func TestUpdateRestartProtectsUnsavedValues(t *testing.T) {
	restarts := mockUpdater(t, nil)
	mr := miniredis.RunT(t)
	mr.Set("draft", "original")
	h := newUIFlow(t)
	s := h.connect(mr.Addr(), "")
	h.selectKey("draft")
	h.fill("Code editor", "unsaved")
	h.a.updateReady = true
	h.ui.Frame()
	h.click("Install and restart")
	if h.a.editGuard == nil || restarts.Load() != 0 {
		t.Fatal("restart discarded edits")
	}
	h.click("Cancel")
	if !s.editorModified() || restarts.Load() != 0 {
		t.Fatal("cancel lost edits")
	}
	h.click("Install and restart")
	h.click("Save and continue")
	h.wait("saved restart", func() bool { return restarts.Load() == 1 })
	if got, _ := mr.Get("draft"); got != "unsaved" {
		t.Fatal("restart preceded the save")
	}
}

// Release CI verifies the actual archives against the public key shipped in
// the app; filenames and checksums alone cannot enable a safe in-app update.
func TestPackagedUpdateAssets(t *testing.T) {
	dir := os.Getenv("KIVRIX_RELEASE_ASSETS")
	if dir == "" {
		t.Skip("set KIVRIX_RELEASE_ASSETS to verify signed updates")
	}
	data, err := os.ReadFile("../../mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Updates struct{ PublicKey, GitHub string }
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	key, err := base64.StdEncoding.DecodeString(cfg.Updates.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		t.Fatal("invalid update public key")
	}
	for _, target := range []string{"darwin-universal", "windows-amd64", "linux-amd64", "linux-arm64"} {
		t.Run(target, func(t *testing.T) {
			manifests, err := filepath.Glob(filepath.Join(dir, "*", "update-"+target+".json"))
			if err != nil || len(manifests) != 1 {
				t.Fatalf("expected one manifest: %v %v", manifests, err)
			}
			data, err := os.ReadFile(manifests[0])
			if err != nil {
				t.Fatal(err)
			}
			var m struct {
				Version, URL, Signature string
				Size                    int64
			}
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			platform := target
			if target == "darwin-universal" {
				platform = "macOS"
			}
			name := "kivrix-" + appVersion + "-" + platform + ".tar.gz"
			url := "https://github.com/" + cfg.Updates.GitHub + "/releases/download/v" + appVersion + "/" + name
			if m.Version != appVersion || m.URL != url {
				t.Fatalf("incorrect manifest: %+v", m)
			}
			if err := verifyUpdateArchive(filepath.Join(filepath.Dir(manifests[0]), name), key, m.Signature, m.Size); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func verifyUpdateArchive(path string, key ed25519.PublicKey, signature string, size int64) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	sum := sha256.New()
	n, err := io.Copy(sum, file)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || n != size || !ed25519.Verify(key, sum.Sum(nil), sig) {
		return fmt.Errorf("invalid signed update: %s", filepath.Base(path))
	}
	return nil
}

func TestUpdateArchiveRejectsTampering(t *testing.T) {
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "update.tar.gz")
	payload := []byte("signed archive fixture")
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(private, sum[:]))
	if err := verifyUpdateArchive(path, public, sig, int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	payload[0] ^= 1
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyUpdateArchive(path, public, sig, int64(len(payload))); err == nil {
		t.Fatal("tampered archive accepted")
	}
}

func TestVersionMatchesConfig(t *testing.T) {
	data, err := os.ReadFile("../../mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Version != appVersion {
		t.Fatalf("mygo.json %q != appVersion %q", cfg.Version, appVersion)
	}
}

func TestSettingsAppearanceBackground(t *testing.T) {
	a := newApp()
	a.prefsPath = filepath.Join(t.TempDir(), "preferences.json")
	a.prefsOpen = true
	a.prefsTab = settingsAppearance
	tt := ui.NewTester(a.settingsView, 820, 620)
	for _, want := range []string{"Appearance", "Language", "Translucent", "Background", "Petal"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	for _, name := range []string{"Meadow", "Tide", "Apricot", "Aurora", "Dune", "Paper"} {
		if err := tt.Click(name); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		saved, err := loadPreferences(a.prefsPath)
		if err != nil || a.prefs.Background != name || saved.Background != name {
			t.Fatalf("background %q: selected %q, saved %q, error %v", name, a.prefs.Background, saved.Background, err)
		}
	}
}

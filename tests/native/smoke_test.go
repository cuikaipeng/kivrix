//go:build nativee2e

// Native startup smoke test. It opens the real application in a desktop session;
// the normal go test ./... suite does not compile or launch it.
package native_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	"github.com/cuikaipeng/kivrix/internal/app"
	"github.com/egoist/mygo"
)

var fixture *miniredis.Miniredis
var scanned = make(chan struct{})

func TestMain(m *testing.M) {
	if os.Getenv("KIVRIX_NATIVE_E2E") != "1" {
		os.Exit(m.Run())
	}
	code := runNative(m)
	os.Exit(code)
}

func runNative(m *testing.M) int {
	dir, err := os.MkdirTemp("", "kivrix-native-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	fixture, err = miniredis.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer fixture.Close()
	fixture.Select(2)
	fixture.Set("native-smoke", "fixture data")
	var once sync.Once
	fixture.Server().SetPreHook(func(_ *server.Peer, cmd string, _ ...string) bool {
		if cmd == "SCAN" {
			once.Do(func() { close(scanned) })
		}
		return false
	})
	// All storage, including the encrypted vault and window state, is temporary.
	for _, name := range []mygo.PathName{mygo.PathAppData, mygo.PathUserData, mygo.PathCache, mygo.PathLogs} {
		mygo.App.SetPath(name, dir)
	}
	mygo.App.SetName("Kivrix Native Test")
	for name, value := range map[string]any{
		"profiles.json": []app.Profile{{ID: "native-test", Name: "Native test", Addr: fixture.Addr(), DB: 2}},
		"preferences.json": app.Preferences{
			Language: "en", Startup: "Last session", LastProfile: "native-test", LastDB: 2,
			CheckUpdates: false,
		},
	} {
		data, err := json.Marshal(value)
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, name), data, 0o600)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	// Keep the event loop alive while the test checks close-time cleanup.
	mygo.App.OnWindowAllClosed(func() {})
	exitCode := make(chan int, 1)
	mygo.App.WhenReady(func() {
		go func() {
			exitCode <- m.Run()
			mygo.App.Quit()
		}()
	})
	app.Main() // the production entry point, on the native main thread
	return <-exitCode
}

func TestNativeStartupRenderAndClose(t *testing.T) {
	if os.Getenv("KIVRIX_NATIVE_E2E") != "1" {
		t.Skip("set KIVRIX_NATIVE_E2E=1 in a desktop session")
	}
	select {
	case <-scanned:
	case <-time.After(10 * time.Second):
		t.Fatal("application did not restore and scan the saved connection")
	}
	windows := mygo.Windows()
	if len(windows) != 1 || !windows[0].IsVisible() {
		t.Fatalf("expected one visible application window; found %d", len(windows))
	}
	w := windows[0]
	checked := make(chan bool, 1)
	w.Update(func() {
		menu := mygo.App.Menu()
		if menu == nil || menu.ItemByID("check-updates") == nil {
			checked <- false
			return
		}
		item := menu.ItemByID("check-updates")
		item.Click(item, w) // development builds report unavailable without networking
		checked <- true
	})
	select {
	case ok := <-checked:
		if !ok {
			t.Fatal("native update menu is missing")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native menu action did not run")
	}
	// CapturePage reads the most recently painted frame. Wait for it to settle
	// after restoration instead of accepting a screenshot of the loading screen.
	w.Invalidate()
	var data []byte
	settled := false
	frames := time.NewTicker(100 * time.Millisecond)
	defer frames.Stop()
	frameDeadline := time.NewTimer(5 * time.Second)
	defer frameDeadline.Stop()
	for !settled {
		select {
		case <-frames.C:
			next, err := w.CapturePage()
			if err != nil {
				t.Fatal(err)
			}
			settled = len(data) > 0 && bytes.Equal(data, next)
			data = next
		case <-frameDeadline.C:
			t.Fatal("native window did not finish painting")
		}
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() < 760 || img.Bounds().Dy() < 520 {
		t.Fatalf("unexpected content size: %v", img.Bounds())
	}
	// Detect empty/solid-color captures without platform-specific golden pixels.
	colors := map[[4]uint32]bool{}
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y += 8 {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x += 8 {
			r, g, b, a := img.At(x, y).RGBA()
			colors[[4]uint32{r, g, b, a}] = true
		}
	}
	if len(colors) < 16 {
		t.Fatalf("native content appears blank: %d sampled colors", len(colors))
	}
	if dir := os.Getenv("KIVRIX_TEST_ARTIFACTS"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, "native-startup.png")
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("native screenshot: %s", file)
	}
	w.Close()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for fixture.CurrentConnectionCount() != 0 {
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatal("closing the native window leaked Redis connections")
		}
	}
	if !w.IsDestroyed() || len(mygo.Windows()) != 0 || fixture.TotalConnectionCount() == 0 {
		t.Fatal("window/connection lifecycle did not complete")
	}
}

//go:build integration

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// SCAN pagination and MONITOR need a real server: miniredis ignores SCAN COUNT
// and does not implement MONITOR. The same UI driver runs against disposable
// Redis instances created by scripts/integration.sh.
func TestIntegrationE2EBrowseAndMonitor(t *testing.T) {
	addr := integrationAddr(t, "REDIS_PILOT_TEST_STANDALONE")
	st := integrationStore(t, ConnConfig{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	prefix := fmt.Sprintf("kivrix-ui-%d-", time.Now().UnixNano())
	keys := make([]string, 0, 150)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := st.DeleteKeys(cleanup, keys...); err != nil {
			t.Errorf("cleaning up test keys: %v", err)
		}
	}()
	for i := 0; i < cap(keys); i++ {
		key := fmt.Sprintf("%s%03d", prefix, i)
		keys = append(keys, key)
		if err := st.SetString(ctx, key, "original"); err != nil {
			t.Fatal(err)
		}
	}
	h := newUIFlow(t)
	h.a.prefs.ScanSize = 5
	h.a.form.advanced.DefaultFilter = prefix + "*"
	s := h.connect(addr, "")
	if s.scanIncomplete || s.keys.count != len(keys) {
		t.Fatalf("automatic scan loaded %d of %d keys", s.keys.count, len(keys))
	}
	h.click("Rescan")
	h.wait("partial automatic scan", func() bool { return s.keys.count > 0 })
	if !s.scanning || s.keys.count >= len(keys) {
		t.Fatal("fixture did not exercise multiple SCAN batches")
	}
	loaded := s.keys.count
	h.click("Cancel scan")
	if s.scanning || !s.scanIncomplete || s.keys.count != loaded || !h.ui.HasText("Scan incomplete") {
		t.Fatal("cancel lost loaded keys or reported an incomplete scan as complete")
	}
	h.click("Rescan")
	h.wait("complete SCAN", func() bool { return !s.scanning && !s.scanIncomplete })
	if s.keys.count != len(keys) {
		t.Fatalf("scan loaded %d of %d keys", s.keys.count, len(keys))
	}
	// SCAN inserts sorted rows above the current scroll anchor. Filter before
	// clicking so the target is visible, rather than an overscanned offscreen row.
	scanID := s.scanID
	h.fill("Filter keys", keys[0])
	h.wait("filtered key", func() bool { return s.scanID > scanID && !s.scanning && s.keys.count == 1 && !s.scanIncomplete })
	h.selectKey(keys[0])
	h.fill("Code editor", "native client edit")
	h.click("Save")
	h.wait("saved value", func() bool { return !s.valueSaving && !s.strEditor.Buffer.Modified() })
	if got, err := st.GetValue(ctx, keys[0]); err != nil || got.Str != "native client edit" {
		t.Fatalf("Redis did not receive the UI edit: %+v %v", got, err)
	}
	h.click("Status")
	h.wait("server status", func() bool { return !s.statusLoading && s.statusRaw != "" })
	if s.infoErr != "" || !h.ui.HasText("Redis") {
		t.Fatalf("status failed: %s", s.infoErr)
	}
	h.click("Monitor")
	h.wait("live monitor output", func() bool {
		// Repeated reads remove a race between MONITOR's handshake and the first
		// command, without a fixed startup sleep or writes to unrelated data.
		st.client.Get(ctx, keys[0])
		for _, line := range s.monitorLines {
			if strings.Contains(line, keys[0]) {
				return true
			}
		}
		return false
	})
	if s.monitorErr != "" || !h.ui.HasText(keys[0]) {
		t.Fatalf("monitor failed: %s", s.monitorErr)
	}
	h.click("Browser")
	if s.monitorRunning || s.monitorCancel != nil || s.statusTimer != nil {
		t.Fatal("leaving a live tool retained its worker/timer")
	}
}

// Child clients are also protected: cluster-wide methods use ForEachMaster
// directly, so guarding only the top-level command path is insufficient.
func TestIntegrationReadOnlyModes(t *testing.T) {
	for _, mode := range []string{"standalone", "sentinel", "cluster"} {
		t.Run(mode, func(t *testing.T) {
			cfg := ConnConfig{Mode: mode, ConnectionOptions: ConnectionOptions{ReadOnly: true}}
			switch mode {
			case "standalone":
				cfg.Addr = integrationAddr(t, "REDIS_PILOT_TEST_STANDALONE")
			case "sentinel":
				cfg.SentinelAddrs = []string{integrationAddr(t, "REDIS_PILOT_TEST_SENTINEL")}
				cfg.MasterName = "mymaster"
			case "cluster":
				cfg.ClusterAddrs = strings.Split(integrationAddr(t, "REDIS_PILOT_TEST_CLUSTER"), ",")
			}
			st := integrationStore(t, cfg)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, _, err := st.ScanPage(ctx, "", "*", 5); err != nil {
				t.Fatal(err)
			}
			if _, err := st.Info(ctx); err != nil {
				t.Fatal(err)
			}
			if err := st.SetString(ctx, "read-only-test", "no"); !errors.Is(err, errReadOnly) {
				t.Fatalf("write: %v", err)
			}
			if err := st.FlushDB(ctx); !errors.Is(err, errReadOnly) {
				t.Fatalf("flush: %v", err)
			}
		})
	}
}

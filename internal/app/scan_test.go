package app

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
)

func TestE2EAutomaticScanAndRetry(t *testing.T) {
	mr := miniredis.RunT(t)
	var fail atomic.Bool
	var scans atomic.Int32
	fail.Store(true)
	// Exercise empty, duplicate and failed SCAN batches deterministically;
	// miniredis otherwise returns every matching key in one batch.
	mr.Server().SetPreHook(func(c *server.Peer, cmd string, args ...string) bool {
		if cmd != "SCAN" {
			return false
		}
		scans.Add(1)
		cursor, keys := "0", []string(nil)
		switch args[0] {
		case "0":
			cursor, keys = "1", []string{"first"}
		case "1":
			cursor = "2"
		case "2":
			if fail.Load() {
				c.WriteError("ERR scan denied")
				return true
			}
			keys = []string{"first", "second"}
		default:
			c.WriteError("ERR unexpected scan cursor")
			return true
		}
		c.WriteLen(2)
		c.WriteBulk(cursor)
		c.WriteStrings(keys)
		return true
	})
	h := newUIFlow(t)
	s := h.connect(mr.Addr(), "")
	if s.keys.count != 1 || !s.scanIncomplete || !s.statusErr || !h.ui.HasText("scan denied") || scans.Load() != 3 {
		t.Fatal("failed scan lost its keys, stopped at an empty batch or hid the error")
	}
	fail.Store(false)
	h.click("Rescan")
	h.wait("automatic scan retry", func() bool { return !s.scanning })
	if s.keys.count != 2 || s.scanIncomplete || s.statusErr || scans.Load() != 6 || !h.ui.HasText("Scan complete") {
		t.Fatal("retry did not load every key once and clear the error")
	}
}

func TestAsyncScanCompletionAfterNavigation(t *testing.T) {
	for _, action := range []string{"cancel", "filter", "database", "close"} {
		t.Run(action, func(t *testing.T) {
			st, mr := testStore(t)
			mr.Set("old", "original")
			mr.Set("fresh", "current")
			mr.DB(1).Set("other", "db1")
			h := newUIFlow(t)
			s := h.a.openSession(Profile{ID: "scan"}, *st, "")
			s.cmdEvents.close()
			s.startScan()
			h.ui.Frame()
			oldReply := h.nextUpdate()
			switch action {
			case "cancel":
				h.click("Cancel scan")
			case "filter":
				s.search = "fresh"
				s.startScan()
				h.wait("new filter", func() bool { return !s.scanning })
			case "database":
				s.switchDB(1)
				h.wait("new database", func() bool { return !s.dbSwitching && !s.scanning })
			case "close":
				h.a.closeSession(0)
			}
			oldReply()
			h.ui.Frame()
			if s.keys.node("old") != nil {
				t.Fatal("stale scan results changed the current key list")
			}
			switch action {
			case "cancel":
				if s.scanning || !s.scanIncomplete || !h.ui.HasText("Scan incomplete") {
					t.Fatal("late completion changed the canceled state")
				}
			case "filter", "database":
				want := "fresh"
				if action == "database" {
					want = "other"
				}
				if s.keys.count != 1 || s.keys.node(want) == nil || s.scanIncomplete || s.scanning {
					t.Fatal("new scan was not completed independently")
				}
			}
		})
	}
}

func TestScanBatchBudgetCancelsLoading(t *testing.T) {
	s := testSession(newApp(), ConnConfig{})
	s.keys.add("kept")
	s.keys.bytes = maxTreeBytes
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.scanCancel, s.scanning = cancel, true
	if s.applyScanBatch(s.scanID, []string{"over-budget", "not-added"}) || ctx.Err() == nil {
		t.Fatal("exceeding the key tree budget did not stop the scan")
	}
	if s.scanning || !s.scanIncomplete || !s.statusErr || s.keys.count != 1 || s.keys.node("kept") == nil {
		t.Fatal("budget stop lost loaded keys or hid the incomplete state")
	}
}

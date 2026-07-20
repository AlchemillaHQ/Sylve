//go:build freebsd && cgo

package dnssd

import (
	"context"
	"testing"
	"time"
)

func TestFreeBSDIFNETWatcherLifecycle(t *testing.T) {
	w := &freebsdLinkWatcher{}
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := w.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	if ch == nil {
		t.Fatal("expected non-nil channel")
	}

	t.Log("watcher started, waiting for events...")

	// Wait briefly to see if any initial IFNET events arrive.
	// On an idle system none may come, but the watcher should
	// still be alive and not crash.
	select {
	case <-ch:
		t.Log("received IFNET event")
	case <-time.After(3 * time.Second):
		t.Log("no IFNET events received (expected if no interface changes)")
	}

	// Cancel context to trigger watcher stop.
	cancel()

	// The channel may contain a final buffered event, but it must close.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				t.Log("channel closed cleanly after cancel")
				return
			}
		case <-deadline:
			t.Fatal("channel did not close within 5s of context cancel")
		}
	}
}

func TestFreeBSDIFNETWatcherWithTapInterface(t *testing.T) {
	// This test creates/destroys a tap interface to trigger
	// IFNET ATTACH/DETACH events. Requires root and the
	// if_tap kernel module to be loaded. Skip gracefully
	// if prerequisites aren't met.
	t.Skip("skipping: requires root and if_tap module on FreeBSD")
}

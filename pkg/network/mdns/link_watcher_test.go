package dnssd

import (
	"context"
	"errors"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

type mockLinkWatcher struct {
	ch chan LinkUpdate
}

func (w *mockLinkWatcher) Subscribe(ctx context.Context) (<-chan LinkUpdate, error) {
	go func() {
		<-ctx.Done()
		close(w.ch)
	}()
	return w.ch, nil
}

func newMockLinkWatcher() *mockLinkWatcher {
	return &mockLinkWatcher{
		ch: make(chan LinkUpdate, 1),
	}
}

func TestLinkUpdateReannounce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		iface, err := loopbackInterface()
		if err != nil {
			t.Fatal(err)
		}

		cfg := Config{
			Name:   "TestLink",
			Type:   "_test._tcp",
			Port:   12345,
			Ifaces: []string{iface.Name},
		}
		sv, err := NewService(cfg)
		if err != nil {
			t.Fatal(err)
		}
		sv.ifaceIPs = map[string][]net.IP{
			iface.Name: {net.IPv4(192, 0, 2, 200)},
		}

		conn := newTestConn()
		conn.iface = iface
		watcher := newMockLinkWatcher()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		responder := newResponder(conn)
		responder.watcher = watcher
		responder.addManaged(sv)
		ready := make(chan error, 1)
		done := make(chan error, 1)
		go func() {
			done <- responder.RespondReady(ctx, ready)
		}()
		if err := <-ready; err != nil {
			t.Fatalf("start responder: %v", err)
		}

		watcher.ch <- LinkUpdate{Up: true}
		first := <-conn.sent
		if !first.Response || len(first.Answer) < 3 {
			t.Fatalf("first link reannouncement = %v", first)
		}
		started := time.Now()
		second := <-conn.sent
		if !second.Response || len(second.Answer) != len(first.Answer) {
			t.Fatalf("second link reannouncement = %v", second)
		}
		if elapsed := time.Since(started); elapsed != time.Second {
			t.Fatalf("logical reannouncement interval = %s, want 1s", elapsed)
		}

		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("stop responder: %v", err)
		}
	})
}

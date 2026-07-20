package dnssd

import (
	"context"
	"net"
	"testing"
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
	cfg := Config{
		Name:   "TestLink",
		Type:   "_test._tcp",
		Port:   12345,
		Ifaces: []string{"lo0"},
	}
	sv, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sv.ifaceIPs = map[string][]net.IP{
		"lo0": {net.IP{192, 168, 0, 200}},
	}

	conn := newTestConn()
	otherConn := newTestConn()
	conn.in = otherConn.out
	conn.out = otherConn.in

	watcher := newMockLinkWatcher()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := newResponder(conn)
	r.watcher = watcher
	r.addManaged(sv)
	go r.Respond(ctx)

	// Verify initial announce: resolve the service
	lookupCtx, lookupCancel := context.WithTimeout(ctx, 3*time.Second)
	defer lookupCancel()

	resolved, err := lookupInstance(lookupCtx, "TestLink._test._tcp.local.", otherConn)
	if err != nil {
		t.Fatalf("initial resolution failed: %v", err)
	}
	if resolved.Name != "TestLink" {
		t.Fatalf("unexpected name: %s", resolved.Name)
	}

	// Send a link update
	select {
	case watcher.ch <- LinkUpdate{Up: true}:
	case <-time.After(time.Second):
		t.Fatal("timeout sending link update")
	}

	// Verify the responder is still responding after link update
	lookupCtx2, lookupCancel2 := context.WithTimeout(ctx, 3*time.Second)
	defer lookupCancel2()

	resolved2, err := lookupInstance(lookupCtx2, "TestLink._test._tcp.local.", otherConn)
	if err != nil {
		t.Fatalf("post-link resolution failed: %v", err)
	}
	if resolved2.Name != "TestLink" {
		t.Fatalf("unexpected name after link update: %s", resolved2.Name)
	}

	t.Log("link update re-announce test passed")
}

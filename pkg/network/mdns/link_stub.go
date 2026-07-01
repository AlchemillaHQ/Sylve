//go:build !linux && !freebsd

package dnssd

import (
	"context"

	"github.com/alchemillahq/sylve/pkg/network/mdns/log"
)

type stubLinkWatcher struct{}

func newPlatformLinkWatcher() LinkWatcher {
	return &stubLinkWatcher{}
}

func (w *stubLinkWatcher) Subscribe(ctx context.Context) (<-chan LinkUpdate, error) {
	log.Info.Println("dnssd: unable to wait for link updates")
	return nil, nil
}

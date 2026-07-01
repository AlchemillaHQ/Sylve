//go:build !linux && !freebsd

package dnssd

import "context"

type stubLinkWatcher struct{}

func newPlatformLinkWatcher() LinkWatcher {
	return &stubLinkWatcher{}
}

func (w *stubLinkWatcher) Subscribe(ctx context.Context) (<-chan LinkUpdate, error) {
	return nil, nil
}

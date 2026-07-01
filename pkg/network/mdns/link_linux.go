package dnssd

import (
	"context"
	"net"

	"github.com/vishvananda/netlink"
)

type linuxLinkWatcher struct{}

func newPlatformLinkWatcher() LinkWatcher {
	return &linuxLinkWatcher{}
}

func (w *linuxLinkWatcher) Subscribe(ctx context.Context) (<-chan LinkUpdate, error) {
	done := make(chan struct{})
	ch := make(chan LinkUpdate, 1)

	nlCh := make(chan netlink.LinkUpdate, 1)
	if err := netlink.LinkSubscribe(nlCh, done); err != nil {
		close(done)
		return nil, err
	}

	go func() {
		defer close(done)
		defer close(ch)

		for {
			select {
			case update := <-nlCh:
				iface, err := net.InterfaceByIndex(int(update.Index))
				if err != nil {
					continue
				}

				up := isInterfaceUpAndRunning(iface)

				select {
				case ch <- LinkUpdate{IfIndex: int(update.Index), Up: up}:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

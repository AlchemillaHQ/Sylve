package dnssd

import (
	"context"
	"net"

	"github.com/alchemillahq/sylve/pkg/network/mdns/log"
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

		log.Debug.Println("waiting for link updates...")

		for {
			select {
			case update := <-nlCh:
				iface, err := net.InterfaceByIndex(int(update.Index))
				if err != nil {
					log.Info.Println(err)
					continue
				}

				up := isInterfaceUpAndRunning(iface)
				if up {
					log.Debug.Printf("interface %s is up", iface.Name)
					addrs, err := iface.Addrs()
					if err == nil {
						log.Debug.Printf("addrs %+v", addrs)
					}
				} else {
					log.Debug.Printf("interface %s is down", iface.Name)
				}

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

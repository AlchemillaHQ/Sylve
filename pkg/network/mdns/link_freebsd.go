//go:build freebsd && cgo

package dnssd

/*
extern int start_ifnet_watcher(void);
extern void stop_ifnet_watcher(void);
*/
import "C"
import (
	"context"

	"github.com/alchemillahq/sylve/pkg/network/mdns/log"
)

var ifnetEvents = make(chan LinkUpdate, 1)

//export onIFNETEvent
func onIFNETEvent(cSystem, cSubsystem, cType, cData *C.char) {
	system := C.GoString(cSystem)

	if system != "IFNET" {
		return
	}

	select {
	case ifnetEvents <- LinkUpdate{}:
	default:
	}
}

type freebsdLinkWatcher struct {
	started bool
}

func newPlatformLinkWatcher() LinkWatcher {
	return &freebsdLinkWatcher{}
}

func (w *freebsdLinkWatcher) Subscribe(ctx context.Context) (<-chan LinkUpdate, error) {
	if !w.started {
		w.started = true
		go func() {
			log.Debug.Println("waiting for link updates (nlsysevent IFNET)...")
			res := C.start_ifnet_watcher()
			if res == -99 {
				log.Info.Println("dnssd: nlsysevent not supported (requires FreeBSD 15+)")
			} else if res != 0 {
				log.Info.Printf("dnssd: IFNET watcher exited with code %d", int(res))
			}
		}()
	}

	ch := make(chan LinkUpdate, 1)

	go func() {
		defer close(ch)

		for {
			select {
			case ev := <-ifnetEvents:
				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				C.stop_ifnet_watcher()
				return
			}
		}
	}()

	return ch, nil
}

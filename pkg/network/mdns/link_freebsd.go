//go:build freebsd && cgo

package dnssd

/*
extern int start_ifnet_watcher(void);
extern void stop_ifnet_watcher(void);
*/
import "C"
import (
	"context"
)

var ifnetEvents = make(chan LinkUpdate, 1)

//export onIFNETEvent
func onIFNETEvent(cSystem, cSubsystem, cType, cData *C.char) {
	if C.GoString(cSystem) != "IFNET" {
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
			res := C.start_ifnet_watcher()
			if res != 0 {
				return
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

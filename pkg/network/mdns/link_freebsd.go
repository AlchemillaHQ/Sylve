//go:build freebsd && cgo

package dnssd

/*
extern void prepare_ifnet_watcher(void);
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
	started   bool
	startDone chan struct{}
}

func newPlatformLinkWatcher() LinkWatcher {
	return &freebsdLinkWatcher{}
}

func (w *freebsdLinkWatcher) Subscribe(ctx context.Context) (<-chan LinkUpdate, error) {
	if !w.started {
		w.started = true
		w.startDone = make(chan struct{})
		C.prepare_ifnet_watcher()
		go func() {
			defer close(w.startDone)
			C.start_ifnet_watcher()
		}()
	}

	ch := make(chan LinkUpdate, 1)

	go func() {
		defer close(ch)
		defer func() {
			C.stop_ifnet_watcher()
			if w.startDone != nil {
				<-w.startDone
			}
		}()

		for {
			select {
			case ev := <-ifnetEvents:
				select {
				case ch <- ev:
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

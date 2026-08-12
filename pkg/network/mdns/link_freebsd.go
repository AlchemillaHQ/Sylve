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
	"fmt"
)

var ifnetEvents = make(chan LinkUpdate, 1)
var ifnetWatcherReady = make(chan int, 1)

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

//export onIFNETWatcherReady
func onIFNETWatcherReady(status C.int) {
	select {
	case ifnetWatcherReady <- int(status):
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
	if w.started {
		return nil, fmt.Errorf("freebsd IFNET watcher already subscribed")
	}
	w.started = true
	w.startDone = make(chan struct{})
	drainIFNETWatcherReady()
	drainIFNETEvents()
	C.prepare_ifnet_watcher()
	go func() {
		defer close(w.startDone)
		C.start_ifnet_watcher()
	}()

	select {
	case status := <-ifnetWatcherReady:
		if err := ctx.Err(); err != nil {
			w.stop()
			return nil, err
		}
		if status != 0 {
			w.stop()
			return nil, fmt.Errorf("start freebsd IFNET watcher: status %d", status)
		}
	case <-ctx.Done():
		w.stop()
		return nil, ctx.Err()
	}

	ch := make(chan LinkUpdate, 1)

	go func() {
		defer close(ch)
		defer w.stop()

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
			case <-w.startDone:
				return
			}
		}
	}()

	return ch, nil
}

func (w *freebsdLinkWatcher) stop() {
	C.stop_ifnet_watcher()
	if w.startDone != nil {
		<-w.startDone
	}
}

func drainIFNETWatcherReady() {
	for {
		select {
		case <-ifnetWatcherReady:
		default:
			return
		}
	}
}

func drainIFNETEvents() {
	for {
		select {
		case <-ifnetEvents:
		default:
			return
		}
	}
}

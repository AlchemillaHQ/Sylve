package dnssd

import (
	"context"
	"net"
)

type LinkUpdate struct {
	IfIndex int
	Up      bool
}

type LinkWatcher interface {
	Subscribe(ctx context.Context) (<-chan LinkUpdate, error)
}

func isInterfaceUpAndRunning(iface *net.Interface) bool {
	return iface.Flags&net.FlagUp == net.FlagUp && iface.Flags&net.FlagRunning == net.FlagRunning
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

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

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"testing"

	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	golibvirt "github.com/digitalocean/go-libvirt"
)

func TestCountRunningManagedVMs(t *testing.T) {
	states := []libvirtServiceInterfaces.DomainState{
		{Domain: "101", State: golibvirt.DomainRunning},
		{Domain: "102", State: golibvirt.DomainShutoff},
		{Domain: "999", State: golibvirt.DomainRunning},
	}
	if got := countRunningManagedVMs([]uint{101, 102}, states); got != 1 {
		t.Fatalf("running managed VMs = %d, want 1", got)
	}
}

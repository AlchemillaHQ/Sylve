// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"testing"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
)

func TestPhysicalDiskIdentities(t *testing.T) {
	disks := []diskServiceInterfaces.DiskInfo{
		{Name: "ada0", Serial: "SERIAL-0", LunID: "LUN-0"},
		{Name: "ada1", Serial: "DUPLICATE"},
		{Name: "ada2", Serial: "DUPLICATE"},
		{Name: "ada3"},
	}
	identities := physicalDiskIdentities(disks)
	if len(identities) != len(disks) {
		t.Fatalf("identities=%v", identities)
	}
	if !identities[0].stable {
		t.Fatalf("identity=%+v", identities[0])
	}
	for _, identity := range identities[1:] {
		if identity.stable {
			t.Fatalf("identity=%+v", identity)
		}
	}
	seen := make(map[string]bool, len(identities))
	for _, identity := range identities {
		if identity.uuid == "" || seen[identity.uuid] {
			t.Fatalf("identities=%v", identities)
		}
		seen[identity.uuid] = true
	}
}

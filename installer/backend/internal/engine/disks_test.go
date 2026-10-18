// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import "testing"

func TestGlabelStatusKeepsExactProviderAndPartition(t *testing.T) {
	const output = "Name Status Components\ngpt/zfs01 N/A nda1p4\ngpt/zfs0 N/A nda0p5.eli\n"
	if got := parseGlabelStatus(output, "gpt/zfs0"); got != "nda0p5.eli" {
		t.Fatalf("provider = %q, want nda0p5.eli", got)
	}
	if got := parseGlabelStatus(output, "gpt/zfs"); got != "" {
		t.Fatalf("partial provider matched: %q", got)
	}
}

func TestISOInstallerMedia(t *testing.T) {
	const output = "Name Status Components\niso9660/SYLVE_15_STABLE N/A cd0\niso9660/SYLVE_16_CURRENT N/A da1\n"
	if got := parseGlabelStatus(output, "iso9660/SYLVE_15_STABLE"); got != "cd0" {
		t.Fatalf("optical provider = %q", got)
	}
	if got := parseGlabelStatus(output, "iso9660/SYLVE_16_CURRENT"); got != "da1" {
		t.Fatalf("USB provider = %q", got)
	}
	disks := []Disk{{Name: "vtbd0", SizeBytes: 32 << 30}}
	if !bootMediaKnown("cd0", disks) || !bootMediaKnown("cd0", nil) {
		t.Fatal("known optical media must not require a writable boot disk in inventory")
	}
	if bootMediaKnown("", disks) || bootMediaKnown("md0.uzip", disks) || bootMediaKnown("cd-invalid", disks) || bootMediaKnown("da1", disks) {
		t.Fatal("unknown installer media must fail closed")
	}
	if !bootMediaKnown("da1", append(disks, Disk{Name: "da1", Boot: true})) {
		t.Fatal("hybrid ISO written to USB must remain protected")
	}
}

func TestPartitionGeometry(t *testing.T) {
	const output = `Geom name: nda0
scheme: GPT
Providers:
1. Name: nda0p1
   Mediasize: 272629760 (260M)
   Sectorsize: 512
   label: efi0
   type: efi
   index: 1
   start: 2048
2. Name: nda0p2
   Mediasize: 8589934592 (8G)
   Sectorsize: 512
   label: existing
   type: freebsd-zfs
   index: 2
   start: 534528
Consumers:
1. Name: nda0
   Mediasize: 34359738368 (32G)

Geom name: another
scheme: GPT
Providers:
1. Name: anotherp1
   Mediasize: 100
   start: 1
`
	for _, sector := range []uint64{0, 512, 4096} {
		scheme, parts := parsePartitions(output, sector)
		if scheme != "GPT" || len(parts) != 2 {
			t.Fatalf("sector=%d: scheme=%s, partitions=%+v", sector, scheme, parts)
		}
		if sector == 0 {
			sector = 512
		}
		if parts[0].StartBytes != 2048*sector || parts[1].StartBytes != 534528*sector {
			t.Fatalf("sector=%d: offsets not converted from sectors: %+v", sector, parts)
		}
		if parts[0].SizeBytes != 272629760 || parts[1].SizeBytes != 8589934592 {
			t.Fatalf("partition sizes changed: %+v", parts)
		}
	}
}

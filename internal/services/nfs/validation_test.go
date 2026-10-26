// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
)

func shareInput(dataset string, clients ...string) nfsModels.NFSShareConfig {
	return nfsModels.NFSShareConfig{Dataset: dataset, Enabled: true, ReadOnly: true, Clients: clients}
}

func TestNormalizeShare(t *testing.T) {
	input := shareInput("1", " 192.0.2.99/24 ", "2001:0db8:0001::/64", "198.51.100.4")
	input.Description = " Documents "
	got, err := normalizeShare(input)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"192.0.2.0/24", "198.51.100.4", "2001:db8:1::/64"}
	if !reflect.DeepEqual(got.Clients, want) || got.Description != "Documents" {
		t.Fatalf("normalized share = %#v", got)
	}
	if input.Clients[0] != " 192.0.2.99/24 " {
		t.Fatal("normalization mutated caller's slice")
	}
}

func TestShareValidationRejectsUnsafeInput(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*nfsModels.NFSShareConfig)
	}{
		{"no clients", func(s *nfsModels.NFSShareConfig) { s.Clients = nil }},
		{"empty client", func(s *nfsModels.NFSShareConfig) { s.Clients = []string{""} }},
		{"hostname", func(s *nfsModels.NFSShareConfig) { s.Clients = []string{"client.example.com"} }},
		{"injection", func(s *nfsModels.NFSShareConfig) { s.Clients = []string{"192.0.2.1\n/map -mapall=root"} }},
		{"world cidr", func(s *nfsModels.NFSShareConfig) { s.Clients = []string{"0.0.0.0/0"} }},
		{"world ipv6", func(s *nfsModels.NFSShareConfig) { s.Clients = []string{"::/0"} }},
		{"scope", func(s *nfsModels.NFSShareConfig) { s.Clients = []string{"fe80::1%em0"} }},
		{"mapped", func(s *nfsModels.NFSShareConfig) { s.Clients = []string{"::ffff:192.0.2.1"} }},
		{"multicast", func(s *nfsModels.NFSShareConfig) { s.Clients = []string{"ff02::1"} }},
		{"unspecified", func(s *nfsModels.NFSShareConfig) { s.Clients = []string{"0.0.0.0"} }},
		{"everyone mixed", func(s *nfsModels.NFSShareConfig) { s.Everyone = true }},
		{"description control character", func(s *nfsModels.NFSShareConfig) { s.Description = "a\x00b" }},
		{"description carriage return", func(s *nfsModels.NFSShareConfig) { s.Description = "a\rb" }},
		{"long description", func(s *nfsModels.NFSShareConfig) { s.Description = strings.Repeat("a", 257) }},
		{"non GUID", func(s *nfsModels.NFSShareConfig) { s.Dataset = "tank/data" }},
		{"zero GUID", func(s *nfsModels.NFSShareConfig) { s.Dataset = "0" }},
		{"noncanonical GUID", func(s *nfsModels.NFSShareConfig) { s.Dataset = "01" }},
		{"too many clients", func(s *nfsModels.NFSShareConfig) { s.Clients = make([]string, 65) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := shareInput("1", "192.0.2.1")
			test.mutate(&input)
			if _, err := normalizeShare(input); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("wanted invalid request, got %v", err)
			}
		})
	}
}

func TestNormalizeShareDescriptionLineBreaks(t *testing.T) {
	for _, description := range []string{" Documents\nRead-only ", " Documents\r\nRead-only "} {
		input := shareInput("1", "192.0.2.1")
		input.Description = description
		share, err := normalizeShare(input)
		if err != nil || share.Description != "Documents\nRead-only" {
			t.Fatalf("multiline description = %q, %v", share.Description, err)
		}
	}
}

func TestClientOverlaps(t *testing.T) {
	for _, clients := range [][]string{
		{"192.0.2.1", "192.0.2.1"},
		{"192.0.2.1", "192.0.2.99/24"},
		{"192.0.2.0/24", "192.0.2.0/25"},
		{"2001:db8::/32", "2001:db8:1::1"},
	} {
		if _, err := normalizeShare(shareInput("1", clients...)); !errors.Is(err, ErrConflict) {
			t.Errorf("overlap %v accepted: %v", clients, err)
		}
	}
	first := nfsModels.NFSShare{ID: 1, NFSShareConfig: shareInput("1", "192.0.2.0/24")}
	second := nfsModels.NFSShare{ID: 2, NFSShareConfig: shareInput("1", "192.0.2.1")}
	if err := validateClientConflicts([]nfsModels.NFSShare{first, second}); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-share overlap accepted: %v", err)
	}
	second.Enabled = false
	if err := validateClientConflicts([]nfsModels.NFSShare{first, second}); err != nil {
		t.Fatalf("disabled share should be ignored: %v", err)
	}
	second.Enabled, second.Dataset = true, "2"
	if err := validateClientConflicts([]nfsModels.NFSShare{first, second}); err != nil {
		t.Fatalf("independent filesystem rejected: %v", err)
	}
}

func TestSortedClientOverlapCheckMatchesPairwiseReference(t *testing.T) {
	random := rand.New(rand.NewPCG(123, 456))
	for range 1000 {
		prefixes := make([]netip.Prefix, random.IntN(80))
		for i := range prefixes {
			if random.IntN(2) == 0 {
				address := netip.AddrFrom4([4]byte{10, byte(random.Uint32()), byte(random.Uint32()), byte(random.Uint32())})
				prefixes[i] = netip.PrefixFrom(address, 8+random.IntN(25)).Masked()
			} else {
				address := [16]byte{0x20, 0x01, 0x0d, 0xb8}
				for j := 4; j < len(address); j++ {
					address[j] = byte(random.Uint32())
				}
				prefixes[i] = netip.PrefixFrom(netip.AddrFrom16(address), 32+random.IntN(97)).Masked()
			}
		}
		want := false
		for i, prefix := range prefixes {
			for _, other := range prefixes[:i] {
				want = want || prefix.Overlaps(other)
			}
		}
		if got := prefixesOverlap(slices.Clone(prefixes)); got != want {
			t.Fatalf("sorted overlap check=%t, pairwise=%t: %v", got, want, prefixes)
		}
	}
}

func BenchmarkClientConflictsAtRuleLimit(b *testing.B) {
	shares := make([]nfsModels.NFSShare, maxShares)
	for i := range shares {
		clients := make([]string, maxClients)
		for j := range clients {
			clients[j] = fmt.Sprintf("10.%d.%d.1", i, j)
		}
		shares[i] = nfsModels.NFSShare{ID: uint(i + 1), NFSShareConfig: shareInput("1", clients...)}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := validateClientConflicts(shares); err != nil {
			b.Fatal(err)
		}
	}
}

func testDataset(guid, name, path string) *gzfs.Dataset {
	return &gzfs.Dataset{
		GUID: guid, Name: name, Pool: "tank", Type: gzfs.DatasetTypeFilesystem, Mountpoint: path,
		Properties: map[string]gzfs.ZFSProperty{
			"guid": {Value: guid}, "type": {Value: "filesystem"}, "mountpoint": {Value: path},
			"mounted": {Value: "yes"}, "sharenfs": {Value: "off"}, "keystatus": {Value: "-"},
		},
	}
}

func TestDatasetEligibility(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*gzfs.Dataset)
	}{
		{"volume", func(d *gzfs.Dataset) { d.Type = gzfs.DatasetTypeVolume }},
		{"snapshot", func(d *gzfs.Dataset) { d.Type = gzfs.DatasetTypeSnapshot }},
		{"unmounted", func(d *gzfs.Dataset) { d.Properties["mounted"] = gzfs.ZFSProperty{Value: "no"} }},
		{"locked", func(d *gzfs.Dataset) { d.Properties["keystatus"] = gzfs.ZFSProperty{Value: "unavailable"} }},
		{"sharenfs", func(d *gzfs.Dataset) { d.Properties["sharenfs"] = gzfs.ZFSProperty{Value: "on"} }},
		{"missing sharenfs", func(d *gzfs.Dataset) { delete(d.Properties, "sharenfs") }},
		{"boot", func(d *gzfs.Dataset) { d.Name = "tank/ROOT/default" }},
		{"guests", func(d *gzfs.Dataset) { d.Name = "tank/sylve/jails/private" }},
		{"root", func(d *gzfs.Dataset) { d.Mountpoint = "/" }},
		{"legacy", func(d *gzfs.Dataset) { d.Mountpoint = "legacy" }},
		{"path traversal", func(d *gzfs.Dataset) { d.Mountpoint = "/mnt/../etc" }},
		{"path injection", func(d *gzfs.Dataset) { d.Mountpoint = "/mnt/a\n/etc" }},
		{"other pool", func(d *gzfs.Dataset) { d.Pool = "other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataset := testDataset("1", "tank/data", "/mnt/data")
			test.mutate(dataset)
			if err := datasetEligible(dataset, []string{"tank"}); !errors.Is(err, ErrConflict) {
				t.Fatalf("unsafe dataset accepted: %v", err)
			}
		})
	}
	if err := datasetEligible(testDataset("1", "tank/data", "/mnt/data"), []string{"tank"}); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeConfig(t *testing.T) {
	for _, protocol := range []string{"v3", "v4", "v3v4"} {
		if _, err := normalizeConfig(nfsModels.NFSConfig{Protocol: protocol}); err != nil {
			t.Fatal(err)
		}
	}
	for _, config := range []nfsModels.NFSConfig{{}, {Protocol: "v2"}, {Protocol: "v3v4 "}, {Protocol: "v4", UDP: true}, {Protocol: "v3", MinThreads: 2, MaxThreads: 1}, {Protocol: "v3", MinThreads: 0, MaxThreads: 10}, {Protocol: "v3", MinThreads: 1, MaxThreads: 257}, {Protocol: "v3", BindAddresses: []string{"0.0.0.0"}}, {Protocol: "v3", BindAddresses: []string{"fe80::1%em0"}}, {Protocol: "v3", BindAddresses: []string{"hostname"}}} {
		if _, err := normalizeConfig(config); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("invalid config %#v accepted", config)
		}
	}
	config, err := normalizeConfig(nfsModels.NFSConfig{Protocol: "v3", BindAddresses: []string{" 2001:0db8::1 ", "192.0.2.1", "192.0.2.1"}})
	if err != nil || !reflect.DeepEqual(config.BindAddresses, []string{"192.0.2.1", "2001:db8::1"}) {
		t.Fatalf("listener normalization = %#v, %v", config, err)
	}
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"errors"
	"strings"
	"testing"
)

func validationDisks(t *testing.T, disks ...Disk) {
	t.Helper()
	oldRead, oldMounted := readDiskInventory, diskIsMounted
	readDiskInventory = func() ([]Disk, error) { return disks, nil }
	diskIsMounted = func(string) (bool, string) { return false, "" }
	t.Cleanup(func() { readDiskInventory, diskIsMounted = oldRead, oldMounted })
}

func storagePlan(storage Storage) Plan {
	return Plan{Hostname: "sylve", RootPassword: "test", AdminPassword: "testpass", Storage: &storage}
}

func TestUFSTargetValidation(t *testing.T) {
	for _, target := range []string{"entire", "free"} {
		if err := validateUFSTarget(target); err != nil {
			t.Fatalf("valid target %q rejected: %v", target, err)
		}
	}
	for _, target := range []string{"free-space", "entire "} {
		t.Run(target, func(t *testing.T) {
			plan := storagePlan(Storage{Mode: "ufs", UFS: UFSStorage{Disk: "nda0", Target: target}})
			plan.DryRun = true
			e := New(plan)
			if err := e.Start(); err == nil || !strings.Contains(err.Error(), "unsupported UFS target") {
				t.Fatalf("Start() = %v, want target rejection before disk access", err)
			}
			if e.Snapshot().Running {
				t.Fatal("rejected plan started the install pipeline")
			}
		})
	}
}

func TestStorageLayoutValidation(t *testing.T) {
	disks := map[string]Disk{
		"nda0": {Name: "nda0", SizeBytes: 16 << 30, SectorSize: 512},
		"nda1": {Name: "nda1", SizeBytes: 8 << 30, SectorSize: 512},
		"nda2": {Name: "nda2", SizeBytes: 32 << 30, SectorSize: 4096},
		"nda3": {Name: "nda3", SizeBytes: 32 << 30, Partitions: []Partition{{Name: "nda3p2", SizeBytes: 12 << 30}}},
	}
	cases := []struct {
		name    string
		storage Storage
		wantErr bool
	}{
		{"plain ZFS fits", Storage{Mode: "zfs", ZFS: ZFSStorage{Swap: "6g", Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"nda0"}}}}}, false},
		{"encrypted ZFS fits", Storage{Mode: "zfs", ZFS: ZFSStorage{Encrypt: true, Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"nda0"}}}}}, false},
		{"encrypted bootpool leaves too little data space", Storage{Mode: "zfs", ZFS: ZFSStorage{Encrypt: true, Swap: "6g", Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"nda0"}}}}}, true},
		{"small raw disk is not enough", Storage{Mode: "zfs", ZFS: ZFSStorage{Swap: "0", Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"nda1"}}}}}, true},
		{"ZFS swap exceeds disk", Storage{Mode: "zfs", ZFS: ZFSStorage{Swap: "40g", Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"nda0"}}}}}, true},
		{"one exhausted stripe member", Storage{Mode: "zfs", ZFS: ZFSStorage{Swap: "8g", Encrypt: true, Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"nda1", "nda2"}}}}}, true},
		{"mirror uses remaining smaller member", Storage{Mode: "zfs", ZFS: ZFSStorage{Swap: "2g", Vdevs: []ZFSVdev{{Type: "mirror", Disks: []string{"nda0", "nda1"}}}}}, true},
		{"stripe aggregates remaining space", Storage{Mode: "zfs", ZFS: ZFSStorage{Swap: "2g", Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"nda0", "nda1"}}}}}, false},
		{"existing partition loses no whole-disk overhead", Storage{Mode: "zfs", ZFS: ZFSStorage{Swap: "8g", Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"nda0", "nda3p2"}}}}}, false},
		{"plain UFS fits", Storage{Mode: "ufs", UFS: UFSStorage{Disk: "nda0", Swap: "4g"}}, false},
		{"encrypted UFS fits", Storage{Mode: "ufs", UFS: UFSStorage{Disk: "nda0", Swap: "4g", Encrypt: true}}, false},
		{"UFS swap leaves too little root", Storage{Mode: "ufs", UFS: UFSStorage{Disk: "nda0", Swap: "8g"}}, true},
		{"UFS swap exceeds disk", Storage{Mode: "ufs", UFS: UFSStorage{Disk: "nda0", Swap: "32g"}}, true},
		{"swap rounded to fit physical sectors", Storage{Mode: "ufs", UFS: UFSStorage{Disk: "nda2", Swap: "1.2 GiB"}}, false},
		{"huge swap rejected", Storage{Mode: "ufs", UFS: UFSStorage{Disk: "nda0", Swap: "18446744073709551616B"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := New(storagePlan(c.storage))
			if err := e.validateStorageLayout(disks); (err != nil) != c.wantErr {
				t.Fatalf("validateStorageLayout() = %v, wantErr %t", err, c.wantErr)
			}
		})
	}
}

func TestRejectBadLayoutBeforeInstall(t *testing.T) {
	validationDisks(t, Disk{Name: "nda0", SizeBytes: 16 << 30, SectorSize: 512})
	for _, mode := range []string{"zfs", "ufs"} {
		t.Run(mode, func(t *testing.T) {
			plan := storagePlan(Storage{Mode: mode,
				ZFS: ZFSStorage{Swap: "32g", Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"nda0"}}}},
				UFS: UFSStorage{Disk: "nda0", Swap: "32g"},
			})
			plan.DryRun = true
			e := New(plan)
			if err := e.Start(); err == nil || !strings.Contains(err.Error(), "overhead") {
				t.Fatalf("Start() = %v, want layout error before starting", err)
			}
			if len(e.Snapshot().Logs) != 0 {
				t.Fatal("rejected plan ran commands")
			}
		})
	}
}

func TestLayoutInventoryFailureStopsInstall(t *testing.T) {
	validationDisks(t)
	readDiskInventory = func() ([]Disk, error) { return nil, errors.New("inventory unavailable") }
	plan := storagePlan(Storage{Mode: "zfs", ZFS: ZFSStorage{Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"nda0"}}}}})
	plan.DryRun = true
	e := New(plan)
	if err := e.Start(); err == nil || !strings.Contains(err.Error(), "inventory unavailable") {
		t.Fatalf("Start() = %v, want inventory error", err)
	}
	if len(e.Snapshot().Logs) != 0 {
		t.Fatal("commands executed without disk inventory")
	}
}

func TestUFSFreeLayout(t *testing.T) {
	disk := Disk{Name: "nda0", Scheme: "GPT", SizeBytes: 32 << 30, SectorSize: 512}
	for _, encrypted := range []bool{false, true} {
		parts, root, err := ufsFreeLayout(disk, 2<<30, encrypted)
		if err != nil {
			t.Fatal(err)
		}
		if len(parts) != 4 || parts[0].Type != "efi" || parts[1].Type != "freebsd-boot" || parts[2].Type != "freebsd-swap" || parts[3].Type != "freebsd-ufs" {
			t.Fatalf("unexpected layout: %+v", parts)
		}
		end := layoutAlignBytes
		for _, part := range parts {
			if part.Start < end || part.Start%layoutAlignBytes != 0 || part.Start+part.Size > disk.SizeBytes-layoutAlignBytes {
				t.Fatalf("unsafe partition placement: %+v", part)
			}
			end = part.Start + part.Size
		}
		wantRoot := parts[3].Size
		if encrypted {
			wantRoot -= 4096
		}
		if root != wantRoot || root < minTargetBytes {
			t.Fatalf("root = %d, want %d", root, wantRoot)
		}
	}

	disk.Partitions = []Partition{
		{Type: "efi", StartBytes: 1 << 20, SizeBytes: 260 << 20},
		{Type: "freebsd-boot", StartBytes: 261 << 20, SizeBytes: 512 << 10},
		{Type: "other", StartBytes: 16 << 30, SizeBytes: 8 << 30},
	}
	parts, _, err := ufsFreeLayout(disk, 2<<30, true)
	if err != nil || len(parts) != 2 {
		t.Fatalf("reuse boot partitions: %+v, %v", parts, err)
	}
	for _, created := range parts {
		for _, existing := range disk.Partitions {
			if created.Start < existing.StartBytes+existing.SizeBytes && existing.StartBytes < created.Start+created.Size {
				t.Fatalf("created partition %+v overlaps %+v", created, existing)
			}
		}
	}
}

func TestUFSFreeLayoutRejectsUnsafeSpace(t *testing.T) {
	cases := []struct {
		name   string
		scheme string
		parts  []Partition
	}{
		{"non-GPT", "MBR", nil},
		{"unknown offset", "GPT", []Partition{{SizeBytes: 1 << 30}}},
		{"out of bounds", "GPT", []Partition{{StartBytes: 16 << 30, SizeBytes: 32 << 30}}},
		{"overlap", "GPT", []Partition{{StartBytes: 1 << 30, SizeBytes: 4 << 30}, {StartBytes: 2 << 30, SizeBytes: 4 << 30}}},
		{"fragmented free space", "GPT", []Partition{{StartBytes: 6 << 30, SizeBytes: 4 << 30}, {StartBytes: 16 << 30, SizeBytes: 10 << 30}}},
		{"existing root label", "GPT", []Partition{{Label: ufsRootLabel, StartBytes: 1 << 20, SizeBytes: 1 << 30}}},
		{"existing swap label", "GPT", []Partition{{Label: ufsSwapLabel, StartBytes: 1 << 20, SizeBytes: 1 << 30}}},
		{"EFI label on unrelated data", "GPT", []Partition{{Type: "other", Label: "efi0", StartBytes: 1 << 20, SizeBytes: 1 << 30}}},
		{"boot label on unrelated data", "GPT", []Partition{{Type: "other", Label: "boot0", StartBytes: 1 << 20, SizeBytes: 1 << 30}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			disk := Disk{Name: "nda0", Scheme: c.scheme, SizeBytes: 32 << 30, Partitions: c.parts}
			if _, _, err := ufsFreeLayout(disk, 2<<30, true); err == nil {
				t.Fatal("unsafe free-space layout accepted")
			}
		})
	}
}

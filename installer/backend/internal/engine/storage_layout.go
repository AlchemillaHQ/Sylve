// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"cmp"
	"fmt"
	"math"
	"slices"
)

const (
	biosBootBytes     = uint64(512 << 10)
	efiBytes          = uint64(260 << 20)
	bootPoolBytes     = uint64(2 << 30)
	layoutAlignBytes  = uint64(1 << 20)
	geliMetadataBytes = uint64(4096)
	minZFSMemberBytes = uint64(64 << 20)
)

type plannedPartition struct {
	Type, Label string
	Start, Size uint64
}

func validateUFSTarget(target string) error {
	if target != "entire" && target != "free" {
		return fmt.Errorf("unsupported UFS target %q — use \"entire\" or \"free\"", target)
	}
	return nil
}

func alignLayoutBytes(size uint64) uint64 {
	return (size + layoutAlignBytes - 1) / layoutAlignBytes * layoutAlignBytes
}

func parseSwapSize(value string) (uint64, error) {
	size, err := parseSizeString(value)
	if err != nil {
		return 0, err
	}
	return alignLayoutBytes(size), nil
}

func validateSwapSize(disk Disk, swap uint64) error {
	if swap > math.MaxInt64 {
		return fmt.Errorf("swap size on %s is too large", disk.Name)
	}
	sector := disk.SectorSize
	if sector == 0 {
		sector = 512
	}
	if swap%sector != 0 {
		return fmt.Errorf("swap size on %s must be a multiple of its %d-byte sector size", disk.Name, sector)
	}
	return nil
}

func wholeDiskDataBytes(disk Disk, swap uint64, bootpool, encrypted bool) (uint64, error) {
	if disk.SizeBytes > math.MaxInt64 {
		return 0, fmt.Errorf("disk %s is too large", disk.Name)
	}
	if err := validateSwapSize(disk, swap); err != nil {
		return 0, err
	}
	reserved := 2*layoutAlignBytes + alignLayoutBytes(biosBootBytes) + efiBytes + alignLayoutBytes(swap)
	if bootpool {
		reserved += bootPoolBytes
	}
	if encrypted {
		reserved += geliMetadataBytes
	}
	if reserved >= disk.SizeBytes {
		return 0, fmt.Errorf("%s has no space for root/data after reserving boot, EFI, swap and encryption overhead", disk.Name)
	}
	return disk.SizeBytes - reserved, nil
}

func ufsFreeLayout(disk Disk, swap uint64, encrypted bool) ([]plannedPartition, uint64, error) {
	if disk.Scheme != "GPT" {
		return nil, 0, fmt.Errorf("%s needs a GPT partition table for a free-space install", disk.Name)
	}
	if disk.SizeBytes <= 2*layoutAlignBytes || disk.SizeBytes > math.MaxInt64 {
		return nil, 0, fmt.Errorf("invalid disk size for %s", disk.Name)
	}
	if err := validateSwapSize(disk, swap); err != nil {
		return nil, 0, err
	}
	parts := slices.Clone(disk.Partitions)
	slices.SortFunc(parts, func(a, b Partition) int {
		return cmp.Compare(a.StartBytes, b.StartBytes)
	})
	hasEFI, hasBoot := false, false
	var previousEnd uint64
	for _, part := range parts {
		if part.Label == ufsRootLabel || part.Label == ufsSwapLabel {
			return nil, 0, fmt.Errorf("%s already carries a %q partition — use the entire disk or prepare free space first", disk.Name, part.Label)
		}
		if part.StartBytes == 0 || part.SizeBytes == 0 || part.StartBytes < previousEnd ||
			part.StartBytes > disk.SizeBytes || part.SizeBytes > disk.SizeBytes-part.StartBytes {
			return nil, 0, fmt.Errorf("cannot determine safe free space on %s: invalid partition geometry", disk.Name)
		}
		previousEnd = part.StartBytes + part.SizeBytes
		hasEFI = hasEFI || part.Type == "efi"
		hasBoot = hasBoot || part.Type == "freebsd-boot"
	}
	var fixed []plannedPartition
	if !hasEFI {
		fixed = append(fixed, plannedPartition{Type: "efi", Label: "efi0", Size: efiBytes})
	}
	if !hasBoot {
		fixed = append(fixed, plannedPartition{Type: "freebsd-boot", Label: "boot0", Size: biosBootBytes})
	}
	if swap > 0 {
		fixed = append(fixed, plannedPartition{Type: "freebsd-swap", Label: ufsSwapLabel, Size: swap})
	}
	for _, created := range fixed {
		for _, existing := range parts {
			if existing.Label == created.Label {
				return nil, 0, fmt.Errorf("%s already carries a %q partition label", disk.Name, created.Label)
			}
		}
	}
	var fixedBytes uint64
	for _, part := range fixed {
		if part.Size > disk.SizeBytes || alignLayoutBytes(part.Size) > disk.SizeBytes-fixedBytes {
			return nil, 0, fmt.Errorf("%s has insufficient free space for boot, EFI and swap", disk.Name)
		}
		fixedBytes += alignLayoutBytes(part.Size)
	}
	metadata := uint64(0)
	if encrypted {
		metadata = geliMetadataBytes
	}
	end := (disk.SizeBytes - layoutAlignBytes) / layoutAlignBytes * layoutAlignBytes
	cursor := layoutAlignBytes
	bestStart, bestSize := uint64(0), uint64(0)
	consider := func(stop uint64) {
		start := alignLayoutBytes(cursor)
		stop = min(stop, end) / layoutAlignBytes * layoutAlignBytes
		if stop > start && stop-start > bestSize {
			bestStart, bestSize = start, stop-start
		}
	}
	for _, part := range parts {
		consider(part.StartBytes)
		cursor = max(cursor, part.StartBytes+part.SizeBytes)
	}
	consider(end)
	if bestSize < fixedBytes+metadata+minTargetBytes {
		return nil, 0, fmt.Errorf("%s needs at least %d GiB of contiguous root space after boot, EFI, swap and encryption overhead", disk.Name, minTargetBytes>>30)
	}
	cursor = bestStart
	for i := range fixed {
		fixed[i].Start = cursor
		cursor += alignLayoutBytes(fixed[i].Size)
	}
	rootSize := bestSize - fixedBytes
	fixed = append(fixed, plannedPartition{Type: "freebsd-ufs", Label: ufsRootLabel, Start: cursor, Size: rootSize})
	return fixed, rootSize - metadata, nil
}

func (e *Engine) validateStorageLayout(byName map[string]Disk) error {
	if e.ufsMode() {
		u := e.storage.UFS
		if err := validateUFSTarget(u.Target); err != nil {
			return err
		}
		disk, ok := byName[u.Disk]
		if !ok {
			return fmt.Errorf("disk %q not found", u.Disk)
		}
		swap, err := parseSwapSize(u.Swap)
		if err != nil {
			return err
		}
		var root uint64
		if u.Target == "free" {
			_, root, err = ufsFreeLayout(disk, swap, u.Encrypt)
		} else {
			root, err = wholeDiskDataBytes(disk, swap, false, u.Encrypt)
		}
		if err != nil {
			return err
		}
		if root < minTargetBytes {
			return fmt.Errorf("%s provides %.1f GiB for UFS root after layout overhead — at least %d GiB is required", disk.Name, float64(root)/(1<<30), minTargetBytes>>30)
		}
		return nil
	}
	z := e.storage.ZFS
	swap, err := parseSwapSize(z.Swap)
	if err != nil {
		return err
	}
	sizes := map[string]int64{}
	for _, member := range e.zfsDisks() {
		var size uint64
		if zfsMemberIsPartition(member) {
			for _, part := range byName[partitionParent(member)].Partitions {
				if part.Name == member {
					size = part.SizeBytes
					break
				}
			}
		} else {
			disk, ok := byName[member]
			if !ok {
				return fmt.Errorf("disk %q not found", member)
			}
			size, err = wholeDiskDataBytes(disk, swap, z.Encrypt, z.Encrypt)
			if err != nil {
				return err
			}
		}
		if size < minZFSMemberBytes || size > math.MaxInt64 {
			return fmt.Errorf("%s needs at least %d MiB for its ZFS data partition after layout overhead", member, minZFSMemberBytes>>20)
		}
		sizes[member] = int64(size)
	}
	if usable := zfsUsableBytes(z.Vdevs, func(member string) int64 { return sizes[member] }); usable < minTargetBytes {
		return fmt.Errorf("the ZFS pool provides %.1f GiB after layout overhead and redundancy — at least %d GiB is required", float64(usable)/(1<<30), minTargetBytes>>30)
	}
	return nil
}

func (e *Engine) validateCurrentLayout() error {
	disks, err := readDiskInventory()
	if err != nil {
		return err
	}
	byName := make(map[string]Disk, len(disks))
	for _, disk := range disks {
		byName[disk.Name] = disk
	}
	return e.validateStorageLayout(byName)
}

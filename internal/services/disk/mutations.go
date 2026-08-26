// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alchemillahq/gzfs"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/internal/logger"
	diskUtils "github.com/alchemillahq/sylve/pkg/disk"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const minimumPartitionSize = 1024 * 1024

func normalizePartitionName(partition string) (string, error) {
	partition = strings.TrimSpace(partition)
	partition = strings.TrimPrefix(partition, "/dev/")
	if partition == "" || partition == "." || partition == ".." || strings.ContainsAny(partition, "/\\\x00") {
		return "", ErrInvalidDiskRequest
	}
	return partition, nil
}

func canonicalDevicePath(device string) string {
	return "/dev/" + strings.TrimPrefix(strings.TrimSpace(device), "/dev/")
}

func normalizedZPoolDevicePath(device string) string {
	device = strings.TrimSpace(device)
	if device == "" {
		return ""
	}
	if !strings.HasPrefix(device, "/") {
		device = canonicalDevicePath(device)
	}
	return filepath.Clean(device)
}

func addZPoolVDEVPaths(devices map[string]string, poolName string, vdev *gzfs.ZPoolVDEV) {
	if vdev == nil {
		return
	}
	if len(vdev.Vdevs) == 0 {
		for _, candidate := range []string{vdev.Path, vdev.Name} {
			if path := normalizedZPoolDevicePath(candidate); path != "" {
				devices[path] = poolName
			}
		}
	}
	for _, child := range vdev.Vdevs {
		addZPoolVDEVPaths(devices, poolName, child)
	}
}

func addZPoolVDEVGroup(devices map[string]string, poolName string, group map[string]*gzfs.ZPoolVDEV) {
	for _, vdev := range group {
		addZPoolVDEVPaths(devices, poolName, vdev)
	}
}

func (s *Service) activeZPoolDevices(ctx context.Context) (map[string]string, error) {
	if s.zpoolDeviceSource != nil {
		return s.zpoolDeviceSource(ctx)
	}
	devices := make(map[string]string)
	if s.GZFS == nil || s.GZFS.Zpool == nil {
		return devices, nil
	}

	pools, err := s.GZFS.Zpool.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active ZFS pools: %w", err)
	}
	for _, pool := range pools {
		if pool == nil {
			continue
		}
		addZPoolVDEVGroup(devices, pool.Name, pool.Vdevs)
		addZPoolVDEVGroup(devices, pool.Name, pool.Logs)
		addZPoolVDEVGroup(devices, pool.Name, pool.L2Cache)
		addZPoolVDEVGroup(devices, pool.Name, pool.Spares)
		addZPoolVDEVGroup(devices, pool.Name, pool.Special)
		addZPoolVDEVGroup(devices, pool.Name, pool.Dedup)
	}
	return devices, nil
}

// ActiveISCSIZPool returns the name of an imported ZFS pool backed by an
// iSCSI disk, if one is present.
func (s *Service) ActiveISCSIZPool(ctx context.Context) (string, error) {
	disks, err := s.physicalDisks()
	if err != nil {
		return "", fmt.Errorf("inspect disk devices: %w", err)
	}
	active, err := s.activeZPoolDevices(ctx)
	if err != nil {
		return "", err
	}
	for _, disk := range disks {
		if !disk.IsISCSI && !strings.Contains(strings.ToLower(disk.Description), "iscsi") {
			continue
		}
		if poolName := zpoolForDevicePaths(active, diskDevicePaths(disk, true)); poolName != "" {
			return poolName, nil
		}
	}
	return "", nil
}

func diskDevicePaths(disk diskServiceInterfaces.DiskInfo, includePartitions bool) []string {
	paths := make([]string, 0, 1+len(disk.Aliases)+len(disk.Partitions))
	paths = append(paths, canonicalDevicePath(disk.Name))
	for _, alias := range disk.Aliases {
		paths = append(paths, canonicalDevicePath(alias))
	}
	if includePartitions {
		for _, partition := range disk.Partitions {
			paths = append(paths, canonicalDevicePath(partition.Name))
			for _, alias := range partition.Aliases {
				paths = append(paths, canonicalDevicePath(alias))
			}
		}
	}
	return paths
}

func partitionDevicePaths(partition diskServiceInterfaces.PartitionInfo) []string {
	paths := make([]string, 0, 1+len(partition.Aliases))
	paths = append(paths, canonicalDevicePath(partition.Name))
	for _, alias := range partition.Aliases {
		paths = append(paths, canonicalDevicePath(alias))
	}
	return paths
}

func zpoolForDevicePaths(active map[string]string, candidates []string) string {
	for _, candidate := range candidates {
		if poolName := active[normalizedZPoolDevicePath(candidate)]; poolName != "" {
			return poolName
		}
	}
	return ""
}

func (s *Service) resolveMutationDisk(device string) (diskServiceInterfaces.DiskInfo, error) {
	name, err := normalizePhysicalDeviceName(device)
	if err != nil {
		return diskServiceInterfaces.DiskInfo{}, invalidDiskRequest("invalid disk device", err)
	}

	disks, err := s.physicalDisks()
	if err != nil {
		return diskServiceInterfaces.DiskInfo{}, diskOperationFailed("failed to inspect disk devices", err)
	}
	for _, disk := range disks {
		if disk.Name == name {
			if disk.MediaSize <= 0 {
				return diskServiceInterfaces.DiskInfo{}, diskOperationFailed("disk has an invalid media size", nil)
			}
			return disk, nil
		}
	}
	return diskServiceInterfaces.DiskInfo{}, diskResourceNotFound("disk device was not found", fmt.Errorf("%w: %s", ErrPhysicalDiskNotFound, name))
}

func (s *Service) ensureWholeDiskMutationAllowed(ctx context.Context, disk diskServiceInterfaces.DiskInfo) error {
	if disk.IsBootDevice {
		return diskOperationConflict("boot disks cannot be modified", nil)
	}
	active, err := s.activeZPoolDevices(ctx)
	if err != nil {
		return diskOperationFailed("failed to inspect active ZFS pools", err)
	}
	if poolName := zpoolForDevicePaths(active, diskDevicePaths(disk, true)); poolName != "" {
		return diskOperationConflict(fmt.Sprintf("disk is in use by ZFS pool %s", poolName), nil)
	}
	return nil
}

func isBootPartition(partition diskServiceInterfaces.PartitionInfo) bool {
	partitionType := strings.ToLower(strings.TrimSpace(partition.Type))
	return strings.Contains(partitionType, "boot") || strings.Contains(partitionType, "efi")
}

func (s *Service) resolveMutationPartition(ctx context.Context, value string) (diskServiceInterfaces.DiskInfo, diskServiceInterfaces.PartitionInfo, error) {
	name, err := normalizePartitionName(value)
	if err != nil {
		return diskServiceInterfaces.DiskInfo{}, diskServiceInterfaces.PartitionInfo{}, invalidDiskRequest("invalid partition device", err)
	}

	disks, err := s.physicalDisks()
	if err != nil {
		return diskServiceInterfaces.DiskInfo{}, diskServiceInterfaces.PartitionInfo{}, diskOperationFailed("failed to inspect disk devices", err)
	}
	for _, disk := range disks {
		for _, partition := range disk.Partitions {
			if partition.Name != name {
				continue
			}
			if isBootPartition(partition) {
				return diskServiceInterfaces.DiskInfo{}, diskServiceInterfaces.PartitionInfo{}, diskOperationConflict("boot and EFI partitions cannot be deleted", nil)
			}
			if _, _, parseErr := diskUtils.ParsePartition(canonicalDevicePath(partition.Name)); parseErr != nil {
				return diskServiceInterfaces.DiskInfo{}, diskServiceInterfaces.PartitionInfo{}, invalidDiskRequest("unsupported partition device", parseErr)
			}
			active, activeErr := s.activeZPoolDevices(ctx)
			if activeErr != nil {
				return diskServiceInterfaces.DiskInfo{}, diskServiceInterfaces.PartitionInfo{}, diskOperationFailed("failed to inspect active ZFS pools", activeErr)
			}
			if poolName := zpoolForDevicePaths(active, partitionDevicePaths(partition)); poolName != "" {
				return diskServiceInterfaces.DiskInfo{}, diskServiceInterfaces.PartitionInfo{}, diskOperationConflict(fmt.Sprintf("partition is in use by ZFS pool %s", poolName), nil)
			}
			return disk, partition, nil
		}
	}
	return diskServiceInterfaces.DiskInfo{}, diskServiceInterfaces.PartitionInfo{}, diskResourceNotFound("partition device was not found", nil)
}

func validatePartitionSizes(sizes []uint64) (uint64, error) {
	if len(sizes) == 0 || len(sizes) > MaxPartitionsPerRequest {
		return 0, invalidDiskRequest("between 1 and 128 partition sizes are required", nil)
	}
	total := uint64(0)
	for _, size := range sizes {
		if size < minimumPartitionSize {
			return 0, invalidDiskRequest("partition sizes must be at least 1 MiB", nil)
		}
		if total > ^uint64(0)-size {
			return 0, invalidDiskRequest("partition size total is too large", nil)
		}
		total += size
	}
	return total, nil
}

func ensurePartitionSpace(disk diskServiceInterfaces.DiskInfo, requested uint64) error {
	mediaSize := uint64(disk.MediaSize)
	allocated := uint64(0)
	for _, partition := range disk.Partitions {
		if partition.Size < 0 || allocated > ^uint64(0)-uint64(partition.Size) {
			return diskOperationFailed("disk partition inventory is invalid", nil)
		}
		allocated += uint64(partition.Size)
	}
	if allocated > mediaSize {
		return diskOperationFailed("disk partition inventory exceeds the media size", nil)
	}
	if requested > mediaSize-allocated {
		return diskOperationConflict("disk does not have enough unallocated space", nil)
	}
	return nil
}

func classifyDiskCommandError(operation, device string, err error) error {
	if err == nil {
		return nil
	}
	diagnostic := strings.ToLower(err.Error())
	switch {
	case strings.Contains(diagnostic, "device busy"), strings.Contains(diagnostic, "resource busy"):
		return diskOperationConflict("disk device is busy", err)
	case strings.Contains(diagnostic, "file exists"), strings.Contains(diagnostic, "already exists"):
		return diskOperationConflict("a partition table already exists on the disk", err)
	case strings.Contains(diagnostic, "no space left"), strings.Contains(diagnostic, "not enough space"):
		return diskOperationConflict("disk does not have enough unallocated space", err)
	case strings.Contains(diagnostic, "no such geom"), strings.Contains(diagnostic, "does not exist"):
		return diskResourceNotFound("disk resource was not found", err)
	}
	logger.L.Error().Err(err).Str("operation", operation).Str("device", device).Msg("disk_operation_failed")
	return diskOperationFailed(operation+" failed", err)
}

func (s *Service) DestroyPartitionTableContext(ctx context.Context, device string) error {
	s.DiskOperationMutex.Lock()
	defer s.DiskOperationMutex.Unlock()

	disk, err := s.resolveMutationDisk(device)
	if err != nil {
		return err
	}
	if err := s.ensureWholeDiskMutationAllowed(ctx, disk); err != nil {
		return err
	}
	path := canonicalDevicePath(disk.Name)
	err = destroyPartitionTable(
		path,
		func(string) (uint64, error) { return uint64(disk.MediaSize), nil },
		diskUtils.DestroyDisk,
		openDiskForWrite,
	)
	return classifyDiskCommandError("clear partition table", disk.Name, err)
}

func (s *Service) DestroyPartitionTable(device string) error {
	return s.DestroyPartitionTableContext(context.Background(), device)
}

func (s *Service) InitializeGPTContext(ctx context.Context, device string) error {
	s.DiskOperationMutex.Lock()
	defer s.DiskOperationMutex.Unlock()

	disk, err := s.resolveMutationDisk(device)
	if err != nil {
		return err
	}
	if err := s.ensureWholeDiskMutationAllowed(ctx, disk); err != nil {
		return err
	}
	path := canonicalDevicePath(disk.Name)
	if s.diskIsGPT(path, disk.SectorSize) {
		return diskOperationConflict("a GPT partition table already exists on the disk", nil)
	}

	output, commandErr := utils.RunCommand("/sbin/gpart", "create", "-s", "gpt", path)
	if commandErr != nil {
		return classifyDiskCommandError("initialize GPT partition table", disk.Name, fmt.Errorf("%w: %s", commandErr, strings.TrimSpace(output)))
	}
	if !strings.Contains(output, fmt.Sprintf("%s created", disk.Name)) {
		unexpected := fmt.Errorf("unexpected gpart output: %s", strings.TrimSpace(output))
		return classifyDiskCommandError("initialize GPT partition table", disk.Name, unexpected)
	}
	return nil
}

func (s *Service) InitializeGPT(device string) error {
	return s.InitializeGPTContext(context.Background(), device)
}

func (s *Service) CreatePartitionsContext(ctx context.Context, device string, sizes []uint64) error {
	s.DiskOperationMutex.Lock()
	defer s.DiskOperationMutex.Unlock()

	requested, err := validatePartitionSizes(sizes)
	if err != nil {
		return err
	}
	disk, err := s.resolveMutationDisk(device)
	if err != nil {
		return err
	}
	if err := s.ensureWholeDiskMutationAllowed(ctx, disk); err != nil {
		return err
	}
	path := canonicalDevicePath(disk.Name)
	if !s.diskIsGPT(path, disk.SectorSize) {
		return diskOperationConflict("disk does not have a GPT partition table", nil)
	}
	if err := ensurePartitionSpace(disk, requested); err != nil {
		return err
	}
	if err := diskUtils.CreatePartitions(path, sizes); err != nil {
		return classifyDiskCommandError("create partitions", disk.Name, err)
	}
	return nil
}

func (s *Service) DeletePartitionContext(ctx context.Context, partitionName string) error {
	s.DiskOperationMutex.Lock()
	defer s.DiskOperationMutex.Unlock()

	disk, partition, err := s.resolveMutationPartition(ctx, partitionName)
	if err != nil {
		return err
	}
	if err := diskUtils.DeletePartition(canonicalDevicePath(partition.Name)); err != nil {
		return classifyDiskCommandError("delete partition", disk.Name, err)
	}
	return nil
}

func (s *Service) CreatePartitions(device string, sizes []uint64) error {
	return s.CreatePartitionsContext(context.Background(), device, sizes)
}

func (s *Service) DeletePartition(partition string) error {
	return s.DeletePartitionContext(context.Background(), partition)
}

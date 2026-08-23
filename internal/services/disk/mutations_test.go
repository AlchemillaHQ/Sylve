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
	"errors"
	"testing"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
)

func mutationTestService(disks []diskServiceInterfaces.DiskInfo, zpoolDevices map[string]string, gpt bool) *Service {
	return &Service{
		physicalDiskSource: func() ([]diskServiceInterfaces.DiskInfo, error) {
			return disks, nil
		},
		zpoolDeviceSource: func(context.Context) (map[string]string, error) {
			return zpoolDevices, nil
		},
		diskGPTSource: func(string, int) bool { return gpt },
	}
}

func TestValidatePartitionSizes(t *testing.T) {
	maxUint64 := ^uint64(0)
	tests := []struct {
		name    string
		sizes   []uint64
		want    uint64
		wantErr bool
	}{
		{name: "valid", sizes: []uint64{minimumPartitionSize, 2 * minimumPartitionSize}, want: 3 * minimumPartitionSize},
		{name: "empty", sizes: nil, wantErr: true},
		{name: "too small", sizes: []uint64{minimumPartitionSize - 1}, wantErr: true},
		{name: "too many", sizes: make([]uint64, MaxPartitionsPerRequest+1), wantErr: true},
		{name: "overflow", sizes: []uint64{maxUint64, minimumPartitionSize}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validatePartitionSizes(tt.sizes)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v; wantErr %t", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidDiskRequest) {
				t.Fatalf("error = %v; want ErrInvalidDiskRequest", err)
			}
			if err == nil && got != tt.want {
				t.Fatalf("total = %d; want %d", got, tt.want)
			}
		})
	}
}

func TestEnsurePartitionSpaceUsesUnallocatedCapacity(t *testing.T) {
	disk := diskServiceInterfaces.DiskInfo{
		MediaSize: 10 * minimumPartitionSize,
		Partitions: []diskServiceInterfaces.PartitionInfo{
			{Size: 7 * minimumPartitionSize},
		},
	}
	if err := ensurePartitionSpace(disk, 3*minimumPartitionSize); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ensurePartitionSpace(disk, 4*minimumPartitionSize); !errors.Is(err, ErrDiskOperationConflict) {
		t.Fatalf("error = %v; want ErrDiskOperationConflict", err)
	}
}

func TestWholeDiskMutationsRejectProtectedResources(t *testing.T) {
	baseDisk := diskServiceInterfaces.DiskInfo{
		Name:       "nda0",
		MediaSize:  16 * minimumPartitionSize,
		SectorSize: 512,
		Partitions: []diskServiceInterfaces.PartitionInfo{
			{Name: "nda0p1", Aliases: []string{"gptid/zfs-member"}, Size: 4 * minimumPartitionSize},
		},
	}

	bootDisk := baseDisk
	bootDisk.IsBootDevice = true
	service := mutationTestService([]diskServiceInterfaces.DiskInfo{bootDisk}, map[string]string{}, true)
	if err := service.DestroyPartitionTableContext(context.Background(), "nda0"); !errors.Is(err, ErrDiskOperationConflict) {
		t.Fatalf("boot disk error = %v; want ErrDiskOperationConflict", err)
	}

	service = mutationTestService([]diskServiceInterfaces.DiskInfo{baseDisk}, map[string]string{"/dev/gptid/zfs-member": "tank"}, true)
	if err := service.CreatePartitionsContext(context.Background(), "nda0", []uint64{minimumPartitionSize}); !errors.Is(err, ErrDiskOperationConflict) {
		t.Fatalf("ZFS disk error = %v; want ErrDiskOperationConflict", err)
	}
}

func TestActiveISCSIZPool(t *testing.T) {
	disks := []diskServiceInterfaces.DiskInfo{
		{Name: "da0", Description: "FreeBSD iSCSI Disk"},
		{Name: "vtbd0", Description: "Virtual Disk"},
	}
	service := mutationTestService(disks, map[string]string{"/dev/da0": "tank"}, true)

	poolName, err := service.ActiveISCSIZPool(context.Background())
	if err != nil {
		t.Fatalf("ActiveISCSIZPool: %v", err)
	}
	if poolName != "tank" {
		t.Fatalf("pool = %q, want tank", poolName)
	}

	service = mutationTestService(disks, map[string]string{"/dev/vtbd0": "guests"}, true)
	poolName, err = service.ActiveISCSIZPool(context.Background())
	if err != nil {
		t.Fatalf("ActiveISCSIZPool without iSCSI member: %v", err)
	}
	if poolName != "" {
		t.Fatalf("pool = %q, want empty", poolName)
	}
}

func TestCreatePartitionsPreflight(t *testing.T) {
	disk := diskServiceInterfaces.DiskInfo{
		Name:       "nda0",
		MediaSize:  8 * minimumPartitionSize,
		SectorSize: 512,
		Partitions: []diskServiceInterfaces.PartitionInfo{
			{Name: "nda0p1", Size: 7 * minimumPartitionSize},
		},
	}

	service := mutationTestService([]diskServiceInterfaces.DiskInfo{disk}, map[string]string{}, false)
	if err := service.CreatePartitionsContext(context.Background(), "nda0", []uint64{minimumPartitionSize}); !errors.Is(err, ErrDiskOperationConflict) {
		t.Fatalf("missing GPT error = %v; want ErrDiskOperationConflict", err)
	}

	service = mutationTestService([]diskServiceInterfaces.DiskInfo{disk}, map[string]string{}, true)
	if err := service.CreatePartitionsContext(context.Background(), "nda0", []uint64{2 * minimumPartitionSize}); !errors.Is(err, ErrDiskOperationConflict) {
		t.Fatalf("space error = %v; want ErrDiskOperationConflict", err)
	}
}

func TestDeletePartitionRejectsProtectedResources(t *testing.T) {
	disk := diskServiceInterfaces.DiskInfo{
		Name:      "nda0",
		MediaSize: 16 * minimumPartitionSize,
		Partitions: []diskServiceInterfaces.PartitionInfo{
			{Name: "nda0p1", Type: "efi", Size: minimumPartitionSize},
			{Name: "nda0p2", Aliases: []string{"gptid/zfs-member"}, Type: "freebsd-zfs", Size: 4 * minimumPartitionSize},
		},
	}
	service := mutationTestService([]diskServiceInterfaces.DiskInfo{disk}, map[string]string{"/dev/gptid/zfs-member": "tank"}, true)

	if err := service.DeletePartitionContext(context.Background(), "nda0p1"); !errors.Is(err, ErrDiskOperationConflict) {
		t.Fatalf("EFI partition error = %v; want ErrDiskOperationConflict", err)
	}
	if err := service.DeletePartitionContext(context.Background(), "nda0p2"); !errors.Is(err, ErrDiskOperationConflict) {
		t.Fatalf("ZFS partition error = %v; want ErrDiskOperationConflict", err)
	}
}

func TestDiskMutationResourceValidation(t *testing.T) {
	service := mutationTestService(nil, map[string]string{}, true)
	if err := service.DestroyPartitionTableContext(context.Background(), "../nda0"); !errors.Is(err, ErrInvalidDiskRequest) {
		t.Fatalf("invalid device error = %v; want ErrInvalidDiskRequest", err)
	}
	if err := service.DestroyPartitionTableContext(context.Background(), "nda0"); !errors.Is(err, ErrDiskResourceNotFound) {
		t.Fatalf("missing disk error = %v; want ErrDiskResourceNotFound", err)
	}
	if err := service.DeletePartitionContext(context.Background(), "nda0p1"); !errors.Is(err, ErrDiskResourceNotFound) {
		t.Fatalf("missing partition error = %v; want ErrDiskResourceNotFound", err)
	}
}

func TestClassifyDiskCommandError(t *testing.T) {
	busy := classifyDiskCommandError("delete partition", "nda0", errors.New("gpart: Device busy"))
	if !errors.Is(busy, ErrDiskOperationConflict) || busy.Error() != "disk device is busy" {
		t.Fatalf("busy error = %v", busy)
	}
	notFound := classifyDiskCommandError("delete partition", "nda0", errors.New("gpart: No such geom"))
	if !errors.Is(notFound, ErrDiskResourceNotFound) {
		t.Fatalf("not found error = %v", notFound)
	}
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestShouldPreserveVMStorageRootDataset(t *testing.T) {
	tests := []struct {
		name           string
		storageType    vmModels.VMStorageType
		deleteRawDisks bool
		deleteVolumes  bool
		want           bool
	}{
		{
			name:           "preserves raw when raw deletion unchecked",
			storageType:    vmModels.VMStorageTypeRaw,
			deleteRawDisks: false,
			deleteVolumes:  true,
			want:           true,
		},
		{
			name:           "does not preserve raw when raw deletion checked",
			storageType:    vmModels.VMStorageTypeRaw,
			deleteRawDisks: true,
			deleteVolumes:  false,
			want:           false,
		},
		{
			name:           "preserves zvol when volume deletion unchecked",
			storageType:    vmModels.VMStorageTypeZVol,
			deleteRawDisks: true,
			deleteVolumes:  false,
			want:           true,
		},
		{
			name:           "does not preserve zvol when volume deletion checked",
			storageType:    vmModels.VMStorageTypeZVol,
			deleteRawDisks: false,
			deleteVolumes:  true,
			want:           false,
		},
		{
			name:           "does not preserve non-zfs storage types",
			storageType:    vmModels.VMStorageTypeDiskImage,
			deleteRawDisks: false,
			deleteVolumes:  false,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldPreserveVMStorageRootDataset(tt.storageType, tt.deleteRawDisks, tt.deleteVolumes)
			if got != tt.want {
				t.Fatalf("expected preserve=%t, got %t", tt.want, got)
			}
		})
	}
}

func TestCleanupVMMACObjects_SkipsTransactionWhenNoMACs(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &networkModels.Object{}, &networkModels.ObjectEntry{}, &networkModels.ObjectResolution{})
	service := &Service{DB: db}

	if err := service.cleanupVMMACObjects(true, nil); err != nil {
		t.Fatalf("expected nil error for empty MAC cleanup, got %v", err)
	}

	if err := db.Create(&networkModels.Object{Name: "mac-1", Type: "Mac"}).Error; err != nil {
		t.Fatalf("expected database to remain writable after no-op MAC cleanup, got %v", err)
	}
}

func TestCleanupVMMACObjects_RemovesMACRecords(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &networkModels.Object{}, &networkModels.ObjectEntry{}, &networkModels.ObjectResolution{})
	service := &Service{DB: db}

	obj := networkModels.Object{Name: "mac-1", Type: "Mac"}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to seed object: %v", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{ObjectID: obj.ID, Value: "02:00:00:00:00:01"}).Error; err != nil {
		t.Fatalf("failed to seed object entry: %v", err)
	}
	if err := db.Create(&networkModels.ObjectResolution{ObjectID: obj.ID, ResolvedIP: "192.0.2.10"}).Error; err != nil {
		t.Fatalf("failed to seed object resolution: %v", err)
	}

	if err := service.cleanupVMMACObjects(true, []uint{obj.ID}); err != nil {
		t.Fatalf("expected cleanup to succeed, got %v", err)
	}

	var objectCount int64
	if err := db.Model(&networkModels.Object{}).Count(&objectCount).Error; err != nil {
		t.Fatalf("failed to count objects: %v", err)
	}
	if objectCount != 0 {
		t.Fatalf("expected object cleanup to delete rows, found %d objects", objectCount)
	}

	var entryCount int64
	if err := db.Model(&networkModels.ObjectEntry{}).Count(&entryCount).Error; err != nil {
		t.Fatalf("failed to count object entries: %v", err)
	}
	if entryCount != 0 {
		t.Fatalf("expected entry cleanup to delete rows, found %d entries", entryCount)
	}

	var resolutionCount int64
	if err := db.Model(&networkModels.ObjectResolution{}).Count(&resolutionCount).Error; err != nil {
		t.Fatalf("failed to count object resolutions: %v", err)
	}
	if resolutionCount != 0 {
		t.Fatalf("expected resolution cleanup to delete rows, found %d resolutions", resolutionCount)
	}
}

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
	"github.com/alchemillahq/sylve/internal/testutil"
)

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

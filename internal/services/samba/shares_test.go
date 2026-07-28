// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"context"
	"strings"
	"testing"

	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestDisableMissingSharesDisablesOnlyMissingDatasets(t *testing.T) {
	svc, runner := newSambaServiceWithMockRunner(t)
	ctx := context.Background()

	shares := []sambaModels.SambaShare{
		{Name: "present", Dataset: "guid-present", Enabled: true, CreateMask: "0664", DirectoryMask: "2775"},
		{Name: "missing", Dataset: "guid-missing", Enabled: true, CreateMask: "0664", DirectoryMask: "2775"},
	}
	if err := svc.DB.Create(&shares).Error; err != nil {
		t.Fatalf("failed creating shares: %v", err)
	}
	addDatasetLookupMocks(t, runner, []mockDataset{{Name: "tank/present", GUID: "guid-present", Mountpoint: "/mnt/present"}})

	if err := svc.DisableMissingShares(ctx); err != nil {
		t.Fatalf("DisableMissingShares failed: %v", err)
	}

	var got []sambaModels.SambaShare
	if err := svc.DB.Order("name ASC").Find(&got).Error; err != nil {
		t.Fatalf("failed loading reconciled shares: %v", err)
	}
	if got[0].Name != "missing" || got[0].Enabled {
		t.Fatalf("missing share was not disabled: %+v", got[0])
	}
	if got[1].Name != "present" || !got[1].Enabled {
		t.Fatalf("present share was disabled: %+v", got[1])
	}
}

func TestShareConfigOmitsDisabledShareWithoutLookingUpDataset(t *testing.T) {
	svc, _ := newSambaServiceWithMockRunner(t)
	share := sambaModels.SambaShare{
		Name: "missing", Dataset: "guid-missing", Enabled: true,
		GuestOk: true, CreateMask: "0664", DirectoryMask: "2775",
	}
	if err := svc.DB.Create(&share).Error; err != nil {
		t.Fatalf("failed creating share: %v", err)
	}
	if err := svc.DB.Model(&share).Update("enabled", false).Error; err != nil {
		t.Fatalf("failed disabling share: %v", err)
	}

	cfg, err := svc.ShareConfig(context.Background())
	if err != nil {
		t.Fatalf("ShareConfig failed for disabled missing share: %v", err)
	}
	if strings.Contains(cfg, "[missing]") {
		t.Fatalf("disabled share was included in config: %s", cfg)
	}
}

func TestUpdateShareCanDisableShareWithMissingDataset(t *testing.T) {
	svc, runner := newSambaServiceWithMockRunner(t)
	share := sambaModels.SambaShare{
		Name: "missing", Dataset: "guid-missing", Enabled: true,
		GuestOk: true, ReadOnly: true, CreateMask: "0664", DirectoryMask: "2775",
	}
	if err := svc.DB.Create(&share).Error; err != nil {
		t.Fatalf("failed creating share: %v", err)
	}
	addDatasetLookupMocks(t, runner, nil)

	originalWriteConfig := sambaWriteConfig
	sambaWriteConfig = func(*Service, context.Context, bool) error { return nil }
	t.Cleanup(func() { sambaWriteConfig = originalWriteConfig })

	enabled := false
	if err := svc.UpdateShare(
		context.Background(), uint(share.ID), share.Name, share.Dataset,
		nil, nil, nil, nil, true, false, "0664", "2775", false, 0, false, nil, &enabled,
	); err != nil {
		t.Fatalf("UpdateShare failed: %v", err)
	}

	if err := svc.DB.First(&share, share.ID).Error; err != nil {
		t.Fatalf("failed loading share: %v", err)
	}
	if share.Enabled {
		t.Fatal("missing share remained enabled")
	}
}

func TestCreateShareReturnsDatasetConflictBeforeDBDuplicate(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &sambaModels.SambaShare{})

	existing := sambaModels.SambaShare{
		Name:    "share-one",
		Dataset: "dataset-guid-1",
		Path:    "",
	}
	if err := dbConn.Create(&existing).Error; err != nil {
		t.Fatalf("failed creating existing share fixture: %v", err)
	}

	svc := &Service{DB: dbConn}

	err := svc.CreateShare(
		context.Background(),
		"share-two",
		"dataset-guid-1",
		nil,
		nil,
		nil,
		nil,
		true,
		false,
		"0664",
		"2775",
		false,
		0,
		false,
		nil,
		true,
	)
	if err == nil {
		t.Fatal("expected dataset conflict error, got nil")
	}
	if err.Error() != "share_with_dataset_exists" {
		t.Fatalf("expected share_with_dataset_exists, got %q", err.Error())
	}
}

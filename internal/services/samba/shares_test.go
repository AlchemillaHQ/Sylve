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

	"github.com/alchemillahq/sylve/internal/db/models"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestSharePrincipalsRequirePAMUsers(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &models.Group{}, &models.User{})
	svc := &Service{DB: dbConn}

	localUser := models.User{Username: "local-admin", Source: "local"}
	pamUser := models.User{Username: "alice", Source: "pam"}
	for _, user := range []*models.User{&localUser, &pamUser} {
		if err := dbConn.Create(user).Error; err != nil {
			t.Fatalf("failed creating user %s: %v", user.Username, err)
		}
	}

	if _, _, _, _, err := svc.loadUsersAndGroupsByIDs([]uint{localUser.ID}, nil, nil, nil); err == nil {
		t.Fatal("expected local user principal to be rejected")
	} else if err.Error() != "user_not_pam:local-admin" {
		t.Fatalf("expected user_not_pam:local-admin, got %q", err.Error())
	}

	if _, _, _, _, err := svc.loadUsersAndGroupsByIDs([]uint{pamUser.ID}, nil, nil, nil); err != nil {
		t.Fatalf("expected PAM user principal to be accepted, got %v", err)
	}
}

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

func TestShareConfigSkipsUnmountedDataset(t *testing.T) {
	svc, runner := newSambaServiceWithMockRunner(t)
	shares := []sambaModels.SambaShare{
		{Name: "mounted", Dataset: "mounted", Enabled: true, GuestOk: true, CreateMask: "0664", DirectoryMask: "2775"},
		{Name: "unmounted", Dataset: "unmounted", Enabled: true, GuestOk: true, CreateMask: "0664", DirectoryMask: "2775"},
	}
	if err := svc.DB.Create(&shares).Error; err != nil {
		t.Fatal(err)
	}
	addDatasetLookupMocks(t, runner, []mockDataset{
		{Name: "tank/mounted", GUID: "mounted", Mountpoint: "/mnt/mounted"},
		{Name: "tank/unmounted", GUID: "unmounted", Mountpoint: "/mnt/unmounted", Mounted: "no"},
	})
	runner.AddCommand("zfs set acltype=nfsv4 aclmode=restricted aclinherit=passthrough tank/mounted", "", "", nil)
	originalRun := sambaRunCommand
	sambaRunCommand = func(command string, args ...string) (string, error) {
		if command != "/bin/setfacl" || args[len(args)-1] != "/mnt/mounted" {
			t.Fatalf("unexpected command for unavailable share: %s %v", command, args)
		}
		return "", nil
	}
	t.Cleanup(func() { sambaRunCommand = originalRun })

	config, err := svc.ShareConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(config, "[mounted]") || strings.Contains(config, "[unmounted]") {
		t.Fatalf("unexpected share configuration: %s", config)
	}
	for _, call := range runner.CallHistory {
		if strings.Contains(call.Cmd, "zfs set ") && strings.HasSuffix(call.Cmd, " tank/unmounted") {
			t.Fatalf("changed properties on unmounted dataset: %s", call.Cmd)
		}
	}
	var stored sambaModels.SambaShare
	if err := svc.DB.First(&stored, shares[1].ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled {
		t.Fatal("unmounted share's saved configuration was disabled")
	}
}

func TestShareMutationsRejectUnmountedDataset(t *testing.T) {
	for _, operation := range []string{"create", "update", "enable"} {
		t.Run(operation, func(t *testing.T) {
			svc, runner := newSambaServiceWithMockRunner(t)
			share := sambaModels.SambaShare{Name: "share", Dataset: "guid", Enabled: true, GuestOk: true, CreateMask: "0664", DirectoryMask: "2775"}
			if operation != "create" {
				if err := svc.DB.Create(&share).Error; err != nil {
					t.Fatal(err)
				}
				if operation == "enable" {
					if err := svc.DB.Model(&share).Update("enabled", false).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			addDatasetLookupMocks(t, runner, []mockDataset{{Name: "tank/share", GUID: "guid", Mountpoint: "/mnt/share", Mounted: "no"}})
			commands := stubExtraShareCommands(t, func(string) error { return nil })
			var err error
			switch operation {
			case "create":
				err = svc.CreateShare(context.Background(), share.Name, share.Dataset, nil, nil, nil, nil, true, false, "0664", "2775", false, 0, false, 70, nil, true, "", nil)
			case "update":
				err = svc.UpdateShare(context.Background(), uint(share.ID), share.Name, share.Dataset, nil, nil, nil, nil, true, false, "0664", "2775", false, 0, false, 70, nil, nil, nil, nil)
			case "enable":
				err = svc.SetShareEnabled(context.Background(), uint(share.ID), true)
			}
			if err == nil || err.Error() != "dataset_not_mounted" {
				t.Fatalf("expected dataset_not_mounted, got %v", err)
			}
			if len(*commands) != 0 {
				t.Fatalf("unmounted dataset ran filesystem commands: %v", *commands)
			}
			for _, call := range runner.CallHistory {
				if strings.Contains(call.Cmd, "zfs set ") {
					t.Fatalf("unmounted dataset properties were changed: %s", call.Cmd)
				}
			}
		})
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
		nil, nil, nil, nil, true, false, "0664", "2775", false, 0, false, 70, nil, &enabled, nil, nil,
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
		70,
		nil,
		true,
		"",
		nil,
	)
	if err == nil {
		t.Fatal("expected dataset conflict error, got nil")
	}
	if err.Error() != "share_with_dataset_exists" {
		t.Fatalf("expected share_with_dataset_exists, got %q", err.Error())
	}
}

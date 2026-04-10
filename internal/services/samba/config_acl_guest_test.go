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
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	gzfstest "github.com/alchemillahq/gzfs/testutil"
	"github.com/alchemillahq/sylve/internal/db/models"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/testutil"
)

type mockDataset struct {
	Name       string
	GUID       string
	Mountpoint string
}

func newSambaServiceWithMockRunner(t *testing.T) (*Service, *gzfstest.MockRunner) {
	t.Helper()

	dbConn := testutil.NewSQLiteTestDB(
		t,
		&models.Group{},
		&sambaModels.SambaSettings{},
		&sambaModels.SambaShare{},
	)

	runner := gzfstest.NewMockRunner()
	client := gzfs.NewClient(gzfs.Options{
		Runner: runner,
	})

	return &Service{
		DB:   dbConn,
		GZFS: client,
	}, runner
}

func addDatasetLookupMocks(t *testing.T, runner *gzfstest.MockRunner, datasets []mockDataset) {
	t.Helper()

	makeResp := func() string {
		resp := map[string]any{
			"output_version": map[string]any{
				"command":    "zfs",
				"vers_major": 0,
				"vers_minor": 0,
			},
			"datasets": map[string]any{},
		}

		dsMap := resp["datasets"].(map[string]any)
		for _, ds := range datasets {
			pool := ds.Name
			if idx := strings.Index(ds.Name, "/"); idx > 0 {
				pool = ds.Name[:idx]
			}

			dsMap[ds.Name] = map[string]any{
				"name": ds.Name,
				"type": string(gzfs.DatasetTypeFilesystem),
				"pool": pool,
				"properties": map[string]any{
					"guid": map[string]any{
						"value": ds.GUID,
						"source": map[string]any{
							"type": "local",
							"data": "-",
						},
					},
					"mountpoint": map[string]any{
						"value": ds.Mountpoint,
						"source": map[string]any{
							"type": "local",
							"data": "-",
						},
					},
				},
			}
		}

		b, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("failed to marshal mock zfs response: %v", err)
		}

		return string(b)
	}

	resp := makeResp()
	runner.AddCommand("zfs get -p -H -o name,value guid -j", resp, "", nil)
	runner.AddCommand("zfs list -o name,origin,used,available,recordsize,mountpoint,compression,type,volsize,quota,referenced,written,logicalused,usedbydataset,guid,mounted,checksum,aclmode,aclinherit,primarycache,volmode,compressratio,atime,dedup,volblocksize,encryption,encryptionroot,keyformat,keylocation -p", resp, "", nil)
}

func TestGlobalConfigMapToGuestIsConditional(t *testing.T) {
	svc, _ := newSambaServiceWithMockRunner(t)

	settings := sambaModels.SambaSettings{
		UnixCharset:        "UTF-8",
		Workgroup:          "WORKGROUP",
		ServerString:       "Sylve SMB Server",
		Interfaces:         "lo0",
		BindInterfacesOnly: true,
	}
	if err := svc.DB.Create(&settings).Error; err != nil {
		t.Fatalf("failed creating samba settings: %v", err)
	}

	cfg, err := svc.GlobalConfig()
	if err != nil {
		t.Fatalf("GlobalConfig failed: %v", err)
	}
	if strings.Contains(cfg, "map to guest = Bad User") {
		t.Fatalf("did not expect map to guest when there are no guest-only shares")
	}

	regular := sambaModels.SambaShare{
		Name:          "regular",
		Dataset:       "guid-regular",
		GuestOk:       false,
		CreateMask:    "0664",
		DirectoryMask: "2775",
	}
	if err := svc.DB.Create(&regular).Error; err != nil {
		t.Fatalf("failed creating regular share: %v", err)
	}

	cfg, err = svc.GlobalConfig()
	if err != nil {
		t.Fatalf("GlobalConfig failed: %v", err)
	}
	if strings.Contains(cfg, "map to guest = Bad User") {
		t.Fatalf("did not expect map to guest when there are only authenticated shares")
	}

	guest := sambaModels.SambaShare{
		Name:          "guest",
		Dataset:       "guid-guest",
		GuestOk:       true,
		CreateMask:    "0664",
		DirectoryMask: "2775",
	}
	if err := svc.DB.Create(&guest).Error; err != nil {
		t.Fatalf("failed creating guest share: %v", err)
	}

	cfg, err = svc.GlobalConfig()
	if err != nil {
		t.Fatalf("GlobalConfig failed: %v", err)
	}
	if !strings.Contains(cfg, "map to guest = Bad User") {
		t.Fatalf("expected map to guest when at least one guest-only share exists")
	}
}

func TestShareConfigGuestOnlyDoesNotEmitUserLists(t *testing.T) {
	svc, runner := newSambaServiceWithMockRunner(t)
	ctx := context.Background()

	share := sambaModels.SambaShare{
		Name:          "public",
		Dataset:       "guid-public",
		GuestOk:       true,
		ReadOnly:      false,
		CreateMask:    "0664",
		DirectoryMask: "2775",
	}
	if err := svc.DB.Create(&share).Error; err != nil {
		t.Fatalf("failed creating share: %v", err)
	}

	addDatasetLookupMocks(t, runner, []mockDataset{
		{Name: "tank/public", GUID: "guid-public", Mountpoint: "/mnt/public"},
	})
	runner.AddCommand("zfs set acltype=nfsv4 aclmode=restricted aclinherit=passthrough tank/public", "", "", nil)

	cfg, err := svc.ShareConfig(ctx)
	if err != nil {
		t.Fatalf("ShareConfig failed: %v", err)
	}

	if !strings.Contains(cfg, "[public]") {
		t.Fatalf("expected share section for public")
	}
	if !strings.Contains(cfg, "\tguest ok = yes\n") {
		t.Fatalf("expected guest ok = yes")
	}
	if !strings.Contains(cfg, "\tguest only = yes\n") {
		t.Fatalf("expected guest only = yes")
	}
	if !strings.Contains(cfg, "\tread only = no\n") {
		t.Fatalf("expected guest-only writable share to emit read only = no")
	}
	if strings.Contains(cfg, "valid users =") {
		t.Fatalf("did not expect valid users for guest-only share")
	}
	if strings.Contains(cfg, "write list =") {
		t.Fatalf("did not expect write list for guest-only share")
	}
	if strings.Contains(cfg, "force user =") {
		t.Fatalf("did not expect force user for guest-only share")
	}
}

func TestShareConfigAuthenticatedEmitsAccessLists(t *testing.T) {
	svc, runner := newSambaServiceWithMockRunner(t)
	ctx := context.Background()

	ro := models.Group{Name: "ro"}
	rw := models.Group{Name: "rw"}
	if err := svc.DB.Create(&ro).Error; err != nil {
		t.Fatalf("failed creating ro group: %v", err)
	}
	if err := svc.DB.Create(&rw).Error; err != nil {
		t.Fatalf("failed creating rw group: %v", err)
	}

	share := sambaModels.SambaShare{
		Name:            "secure",
		Dataset:         "guid-secure",
		GuestOk:         false,
		ReadOnly:        false,
		ReadOnlyGroups:  []models.Group{ro},
		WriteableGroups: []models.Group{rw},
		CreateMask:      "0664",
		DirectoryMask:   "2775",
	}
	if err := svc.DB.Create(&share).Error; err != nil {
		t.Fatalf("failed creating share: %v", err)
	}

	addDatasetLookupMocks(t, runner, []mockDataset{
		{Name: "tank/secure", GUID: "guid-secure", Mountpoint: "/mnt/secure"},
	})
	runner.AddCommand("zfs set acltype=nfsv4 aclmode=restricted aclinherit=passthrough tank/secure", "", "", nil)

	cfg, err := svc.ShareConfig(ctx)
	if err != nil {
		t.Fatalf("ShareConfig failed: %v", err)
	}

	if !strings.Contains(cfg, "\tguest ok = no\n") {
		t.Fatalf("expected guest ok = no for authenticated share")
	}
	if !strings.Contains(cfg, "valid users = @ro @rw") {
		t.Fatalf("expected valid users for authenticated share, got:\n%s", cfg)
	}
	if !strings.Contains(cfg, "write list = @rw") {
		t.Fatalf("expected write list for authenticated share, got:\n%s", cfg)
	}
	if !strings.Contains(cfg, "\tread only = yes\n") {
		t.Fatalf("expected read only = yes for split read/write groups")
	}
}

func TestShareConfigBestEffortWhenACLPropertySetFails(t *testing.T) {
	svc, runner := newSambaServiceWithMockRunner(t)
	ctx := context.Background()

	share := sambaModels.SambaShare{
		Name:          "public",
		Dataset:       "guid-public",
		GuestOk:       true,
		ReadOnly:      false,
		CreateMask:    "0664",
		DirectoryMask: "2775",
	}
	if err := svc.DB.Create(&share).Error; err != nil {
		t.Fatalf("failed creating share: %v", err)
	}

	addDatasetLookupMocks(t, runner, []mockDataset{
		{Name: "tank/public", GUID: "guid-public", Mountpoint: "/mnt/public"},
	})
	runner.AddCommand("zfs set acltype=nfsv4 aclmode=restricted aclinherit=passthrough tank/public", "", "failed", errors.New("set failed"))

	cfg, err := svc.ShareConfig(ctx)
	if err != nil {
		t.Fatalf("ShareConfig should not fail when ACL property set fails in best-effort mode: %v", err)
	}
	if !strings.Contains(cfg, "[public]") {
		t.Fatalf("expected share config to still be generated")
	}
}

func TestCreateShareRejectsGuestOnlyWithPrincipals(t *testing.T) {
	svc, _ := newSambaServiceWithMockRunner(t)

	err := svc.CreateShare(
		context.Background(),
		"public",
		"guid-public",
		nil,
		nil,
		nil,
		[]uint{1},
		true,
		false,
		"0664",
		"2775",
		false,
		0,
	)
	if err == nil {
		t.Fatal("expected error for guest-only share with principals")
	}
	if !strings.Contains(err.Error(), "guest_only_share_cannot_have_principals") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateShareRejectsGuestOnlyWithPrincipals(t *testing.T) {
	svc, _ := newSambaServiceWithMockRunner(t)

	existing := sambaModels.SambaShare{
		Name:          "private",
		Dataset:       "guid-private",
		GuestOk:       false,
		CreateMask:    "0664",
		DirectoryMask: "2775",
	}
	if err := svc.DB.Create(&existing).Error; err != nil {
		t.Fatalf("failed creating existing share: %v", err)
	}

	err := svc.UpdateShare(
		context.Background(),
		uint(existing.ID),
		existing.Name,
		existing.Dataset,
		nil,
		nil,
		nil,
		[]uint{1},
		true,
		false,
		"0664",
		"2775",
		false,
		0,
	)
	if err == nil {
		t.Fatal("expected error for guest-only share with principals")
	}
	if !strings.Contains(err.Error(), "guest_only_share_cannot_have_principals") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateShareFailsWhenACLPropertyEnforcementFails(t *testing.T) {
	svc, runner := newSambaServiceWithMockRunner(t)
	ctx := context.Background()

	addDatasetLookupMocks(t, runner, []mockDataset{
		{Name: "tank/public", GUID: "guid-public", Mountpoint: "/mnt/public"},
	})
	runner.AddCommand("zfs set acltype=nfsv4 aclmode=restricted aclinherit=passthrough tank/public", "", "failed", errors.New("set failed"))

	err := svc.CreateShare(
		ctx,
		"public",
		"guid-public",
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
	)
	if err == nil {
		t.Fatal("expected ACL enforcement failure")
	}
	if !strings.Contains(err.Error(), "failed_to_enforce_samba_dataset_acl_properties") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateShareWriteWinsForOverlappingGroupPermissions(t *testing.T) {
	if _, err := os.Stat("/bin/setfacl"); err != nil {
		t.Skip("setfacl is not available on this test host")
	}

	svc, runner := newSambaServiceWithMockRunner(t)
	ctx := context.Background()

	group := models.Group{Name: "staff"}
	if err := svc.DB.Create(&group).Error; err != nil {
		t.Fatalf("failed creating group: %v", err)
	}

	addDatasetLookupMocks(t, runner, []mockDataset{
		{Name: "tank/public", GUID: "guid-public", Mountpoint: "/mnt/public"},
	})
	runner.AddCommand("zfs set acltype=nfsv4 aclmode=restricted aclinherit=passthrough tank/public", "", "", nil)
	runner.AddCommand("/bin/setfacl -m g:staff:modify_set:fd:allow /mnt/public", "", "", nil)

	err := svc.CreateShare(
		ctx,
		"public",
		"guid-public",
		nil,
		nil,
		[]uint{group.ID},
		[]uint{group.ID},
		false,
		false,
		"0664",
		"2775",
		false,
		0,
	)
	if err != nil {
		t.Fatalf("CreateShare failed: %v", err)
	}

	var share sambaModels.SambaShare
	if err := svc.DB.Preload("ReadOnlyGroups").Preload("WriteableGroups").First(&share).Error; err != nil {
		t.Fatalf("failed loading created share: %v", err)
	}

	if len(share.ReadOnlyGroups) != 0 {
		t.Fatalf("expected overlapping read group to be removed, got %d read groups", len(share.ReadOnlyGroups))
	}
	if len(share.WriteableGroups) != 1 || share.WriteableGroups[0].ID != group.ID {
		t.Fatalf("expected write group to be retained after normalization")
	}
}

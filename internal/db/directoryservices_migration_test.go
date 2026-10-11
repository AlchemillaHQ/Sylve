// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"reflect"
	"testing"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestDirectoryPermissionsMigrationPreservesExistingShares(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t)
	if err := db.Exec(`CREATE TABLE samba_shares (id INTEGER PRIMARY KEY, name TEXT, dataset TEXT, enabled NUMERIC)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO samba_shares (id, name, dataset, enabled) VALUES (1, 'legacy', 'legacy-guid', 0)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&sambaModels.SambaShare{}); err != nil {
		t.Fatal(err)
	}
	var share sambaModels.SambaShare
	if err := db.First(&share, 1).Error; err != nil {
		t.Fatal(err)
	}
	if share.Name != "legacy" || share.Dataset != "legacy-guid" || share.Enabled || len(share.DirectoryPermissions.Read)+len(share.DirectoryPermissions.Write) != 0 {
		t.Fatalf("migration changed existing share configuration: %+v", share)
	}
	permissions := directoryModels.SharePermissions{
		Read:  []directoryModels.Principal{{SID: "S-1-5-21-111-222-333-1100", Name: "EXAMPLE\\alice", Kind: "user", UnixID: 1001100}},
		Write: []directoryModels.Principal{{SID: "S-1-5-21-111-222-333-2100", Name: "EXAMPLE\\File Users", Kind: "group", UnixID: 1002100}},
	}
	share.DirectoryPermissions = permissions
	if err := db.Save(&share).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&sambaModels.SambaShare{}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&share, 1).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(share.DirectoryPermissions, permissions) {
		t.Fatalf("migration lost directory identity metadata: %+v", share.DirectoryPermissions)
	}
}

func TestSavedADCredentialMigrationPreservesExistingMembership(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t)
	if err := db.Exec(`CREATE TABLE active_directories (id INTEGER PRIMARY KEY, domain TEXT, computer_name TEXT, joined NUMERIC, enabled NUMERIC)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO active_directories (id, domain, computer_name, joined, enabled) VALUES (1, 'EXAMPLE.COM', 'SYLVE', 1, 1)`).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.AutoMigrate(&directoryModels.ActiveDirectory{}); err != nil {
			t.Fatal(err)
		}
		var ad directoryModels.ActiveDirectory
		if err := db.First(&ad, 1).Error; err != nil {
			t.Fatal(err)
		}
		if ad.Domain != "EXAMPLE.COM" || ad.ComputerName != "SYLVE" || !ad.Joined || !ad.Enabled || ad.JoinUsername != "" || len(ad.JoinPasswordCiphertext) != 0 || ad.JoinPasswordConfigured || ad.SkipDNSRegistration || ad.JoinWarning != "" {
			t.Fatal("credential migration modified existing domain membership")
		}
	}
}

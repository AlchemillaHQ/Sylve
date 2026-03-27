// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func requireSystemUUIDOrSkip(t *testing.T) {
	t.Helper()
	if _, err := utils.GetSystemUUID(); err != nil {
		t.Skipf("system uuid unavailable in test environment: %v", err)
	}
}

func TestUpdateNameRenamesJailAndTemplateSourceName(t *testing.T) {
	requireSystemUUIDOrSkip(t)
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.JailTemplate{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)
	svc := &Service{DB: db}

	jail := jailModels.Jail{CTID: 601, Name: "jail-old"}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("failed seeding jail: %v", err)
	}

	tpl := jailModels.JailTemplate{
		Name:           "template-jail",
		SourceJailName: "jail-old",
		SourceJailCTID: jail.CTID,
		Pool:           "zroot",
		RootDataset:    "zroot/sylve/templates/jail/601",
	}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatalf("failed seeding jail template: %v", err)
	}

	ctid, err := svc.UpdateName(jail.ID, "jail-new")
	if err != nil {
		t.Fatalf("UpdateName failed: %v", err)
	}
	if ctid != jail.CTID {
		t.Fatalf("expected returned ctid %d, got %d", jail.CTID, ctid)
	}

	var refreshedJail jailModels.Jail
	if err := db.First(&refreshedJail, jail.ID).Error; err != nil {
		t.Fatalf("failed reading jail: %v", err)
	}
	if refreshedJail.Name != "jail-new" {
		t.Fatalf("expected jail name jail-new, got %q", refreshedJail.Name)
	}

	var refreshedTpl jailModels.JailTemplate
	if err := db.First(&refreshedTpl, tpl.ID).Error; err != nil {
		t.Fatalf("failed reading jail template: %v", err)
	}
	if refreshedTpl.SourceJailName != "jail-new" {
		t.Fatalf("expected template source jail name jail-new, got %q", refreshedTpl.SourceJailName)
	}
	if refreshedTpl.SourceJailCTID != jail.CTID {
		t.Fatalf("expected template source jail ctid %d, got %d", jail.CTID, refreshedTpl.SourceJailCTID)
	}
}

func TestUpdateNameRejectsInvalidOrDuplicateName(t *testing.T) {
	requireSystemUUIDOrSkip(t)
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)
	svc := &Service{DB: db}

	jailA := jailModels.Jail{CTID: 701, Name: "jail-a"}
	if err := db.Create(&jailA).Error; err != nil {
		t.Fatalf("failed seeding jail-a: %v", err)
	}
	jailB := jailModels.Jail{CTID: 702, Name: "jail-b"}
	if err := db.Create(&jailB).Error; err != nil {
		t.Fatalf("failed seeding jail-b: %v", err)
	}

	if _, err := svc.UpdateName(jailA.ID, "invalid name"); err == nil || !strings.Contains(err.Error(), "invalid_vm_name") {
		t.Fatalf("expected invalid_vm_name, got %v", err)
	}

	if _, err := svc.UpdateName(jailA.ID, "jail-b"); err == nil || !strings.Contains(err.Error(), "jail_name_already_in_use") {
		t.Fatalf("expected jail_name_already_in_use, got %v", err)
	}
}

func TestUpdateNameDeniedWhenReplicationLeaseNotOwned(t *testing.T) {
	requireSystemUUIDOrSkip(t)
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)
	svc := &Service{DB: db}

	jail := jailModels.Jail{CTID: 801, Name: "jail-protected"}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("failed seeding jail: %v", err)
	}

	policy := clusterModels.ReplicationPolicy{
		Name:            "jail-policy",
		GuestType:       clusterModels.ReplicationGuestTypeJail,
		GuestID:         jail.CTID,
		SourceNodeID:    "node-a",
		ActiveNodeID:    "node-a",
		OwnerEpoch:      1,
		SourceMode:      clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode:    clusterModels.ReplicationFailbackManual,
		FailoverMode:    clusterModels.ReplicationFailoverManual,
		CronExpr:        "* * * * *",
		Enabled:         true,
		TransitionState: clusterModels.ReplicationTransitionStateNone,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("failed seeding replication policy: %v", err)
	}

	lease := clusterModels.ReplicationLease{
		PolicyID:    policy.ID,
		GuestType:   clusterModels.ReplicationGuestTypeJail,
		GuestID:     jail.CTID,
		OwnerNodeID: "other-node",
		OwnerEpoch:  policy.OwnerEpoch,
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		Version:     1,
	}
	if err := db.Create(&lease).Error; err != nil {
		t.Fatalf("failed seeding replication lease: %v", err)
	}

	if _, err := svc.UpdateName(jail.ID, "jail-new"); err == nil || !strings.Contains(err.Error(), "replication_lease_not_owned") {
		t.Fatalf("expected replication_lease_not_owned, got %v", err)
	}
}

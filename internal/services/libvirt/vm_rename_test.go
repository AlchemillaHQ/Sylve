// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func requireSystemUUIDOrSkip(t *testing.T) {
	t.Helper()
	if _, err := utils.GetSystemUUID(); err != nil {
		t.Skipf("system uuid unavailable in test environment: %v", err)
	}
}

func TestUpdateNameRenamesVMAndTemplateSourceName(t *testing.T) {
	requireSystemUUIDOrSkip(t)
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&vmModels.VMTemplate{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)
	svc := &Service{DB: db}

	vm := vmModels.VM{RID: 301, Name: "vm-old"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed seeding vm: %v", err)
	}

	tpl := vmModels.VMTemplate{
		Name:         "template-web",
		SourceVMName: "vm-old",
		SourceVMRID:  vm.RID,
	}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatalf("failed seeding vm template: %v", err)
	}

	if err := svc.UpdateName(vm.RID, "vm-new"); err != nil {
		t.Fatalf("UpdateName failed: %v", err)
	}

	var refreshedVM vmModels.VM
	if err := db.Where("rid = ?", vm.RID).First(&refreshedVM).Error; err != nil {
		t.Fatalf("failed reading vm: %v", err)
	}
	if refreshedVM.Name != "vm-new" {
		t.Fatalf("expected vm name vm-new, got %q", refreshedVM.Name)
	}

	var refreshedTpl vmModels.VMTemplate
	if err := db.First(&refreshedTpl, tpl.ID).Error; err != nil {
		t.Fatalf("failed reading vm template: %v", err)
	}
	if refreshedTpl.SourceVMName != "vm-new" {
		t.Fatalf("expected template source vm name vm-new, got %q", refreshedTpl.SourceVMName)
	}
	if refreshedTpl.SourceVMRID != vm.RID {
		t.Fatalf("expected template source vm rid %d, got %d", vm.RID, refreshedTpl.SourceVMRID)
	}
}

func TestUpdateNameRejectsInvalidOrDuplicateName(t *testing.T) {
	requireSystemUUIDOrSkip(t)
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)
	svc := &Service{DB: db}

	if err := db.Create(&vmModels.VM{RID: 401, Name: "vm-a"}).Error; err != nil {
		t.Fatalf("failed seeding vm-a: %v", err)
	}
	if err := db.Create(&vmModels.VM{RID: 402, Name: "vm-b"}).Error; err != nil {
		t.Fatalf("failed seeding vm-b: %v", err)
	}

	if err := svc.UpdateName(401, "invalid name"); err == nil || !strings.Contains(err.Error(), "invalid_vm_name") {
		t.Fatalf("expected invalid_vm_name, got %v", err)
	}

	if err := svc.UpdateName(401, "vm-b"); err == nil || !strings.Contains(err.Error(), "vm_name_already_in_use") {
		t.Fatalf("expected vm_name_already_in_use, got %v", err)
	}
}

func TestUpdateNameDeniedWhenReplicationLeaseNotOwned(t *testing.T) {
	requireSystemUUIDOrSkip(t)
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)
	svc := &Service{DB: db}

	vm := vmModels.VM{RID: 501, Name: "vm-protected"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed seeding vm: %v", err)
	}

	policy := clusterModels.ReplicationPolicy{
		Name:            "vm-policy",
		GuestType:       clusterModels.ReplicationGuestTypeVM,
		GuestID:         vm.RID,
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
		GuestType:   clusterModels.ReplicationGuestTypeVM,
		GuestID:     vm.RID,
		OwnerNodeID: "other-node",
		OwnerEpoch:  policy.OwnerEpoch,
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		Version:     1,
	}
	if err := db.Create(&lease).Error; err != nil {
		t.Fatalf("failed seeding replication lease: %v", err)
	}

	if err := svc.UpdateName(vm.RID, "vm-new"); err == nil || !strings.Contains(err.Error(), "replication_lease_not_owned") {
		t.Fatalf("expected replication_lease_not_owned, got %v", err)
	}
}

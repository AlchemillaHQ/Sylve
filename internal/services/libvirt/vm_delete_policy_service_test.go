// SPDX-License-Identifier: BSD-2-Clause

package libvirt

import (
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
)

func enableVMDeletePolicySchema(t *testing.T, service *Service) {
	t.Helper()
	if err := service.DB.AutoMigrate(&clusterModels.ReplicationPolicy{}); err != nil {
		t.Fatalf("migrate replication policy: %v", err)
	}
	replicationguard.MarkPolicySchemaReady(service.DB)
}

func seedVMDeletePolicy(t *testing.T, service *Service, rid uint) {
	t.Helper()
	if err := service.DB.Create(&clusterModels.ReplicationPolicy{
		Name:      "vm-delete-policy",
		GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID:   rid,
		Enabled:   false,
	}).Error; err != nil {
		t.Fatalf("seed replication policy: %v", err)
	}
}

func TestRemoveVMServiceBlocksDisabledPolicyBeforeRuntime(t *testing.T) {
	db := newVMDeleteTestDB(t)
	seed := seedVMDeleteGraph(t, db, 780, "tank", false)
	service := &Service{DB: db}
	enableVMDeletePolicySchema(t, service)
	seedVMDeletePolicy(t, service, seed.VM.RID)

	runtimeCalled := false
	_, err := service.removeVMWithWarnings(
		seed.VM.RID, false, false, false, t.Context(),
		func(uint) error {
			runtimeCalled = true
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "guest_delete_requires_replication_policy_removed") {
		t.Fatalf("delete error = %v", err)
	}
	if runtimeCalled {
		t.Fatal("runtime was touched before replication-policy rejection")
	}
}

func TestRemoveVMTransactionRevalidatesPolicy(t *testing.T) {
	db := newVMDeleteTestDB(t)
	seed := seedVMDeleteGraph(t, db, 781, "tank", false)
	service := &Service{DB: db}
	enableVMDeletePolicySchema(t, service)

	_, err := service.removeVMWithWarnings(
		seed.VM.RID, false, false, false, t.Context(),
		func(uint) error {
			seedVMDeletePolicy(t, service, seed.VM.RID)
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "guest_delete_requires_replication_policy_removed") {
		t.Fatalf("delete error = %v", err)
	}
	assertVMDeleteGraphCounts(t, db, seed, 1)
}

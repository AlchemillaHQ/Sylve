// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
	"gorm.io/gorm"
)

func setReplicationGuardZFSProperty(t *testing.T, dataset, property, value string) {
	t.Helper()
	if output, err := exec.Command("zfs", "set", property+"="+value, dataset).CombinedOutput(); err != nil {
		t.Fatalf("zfs set %s=%s %s: %v\n%s", property, value, dataset, err, string(output))
	}
}

func newReplicationGuardService(t *testing.T, pool string, client *gzfs.Client) *Service {
	db := testutil.NewSQLiteTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationGuestOperation{},
		&vmModels.VM{},
		&vmModels.Storage{},
		&vmModels.VMStorageDataset{},
	)
	vm := vmModels.VM{RID: 701, Name: "guarded-vm"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("create vm: %v", err)
	}
	if err := db.Create(&vmModels.Storage{
		VMID: vm.ID, Name: "disk-0", Type: vmModels.VMStorageTypeZVol, Pool: pool, Enable: true,
	}).Error; err != nil {
		t.Fatalf("create vm storage: %v", err)
	}
	if err := db.Create(&clusterModels.ReplicationPolicy{
		ID: 7, Name: "guarded", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: vm.RID,
		SourceNodeID: "node-a", ActiveNodeID: "node-a", OwnerEpoch: 1,
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "0 * * * *", Enabled: true,
		ProtectionState: clusterModels.ReplicationProtectionStateArmed,
		TransitionState: clusterModels.ReplicationTransitionStateNone,
	}).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}
	return &Service{DB: db, GZFS: client}
}

func TestReplicationDatasetGuardProtectsActiveMigrationWithPolicyDisabled(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	svc := newReplicationGuardService(t, pool, client)
	if err := svc.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", 7).Updates(map[string]any{
		"enabled": false, "protection_state": clusterModels.ReplicationProtectionStateUnprotected,
	}).Error; err != nil {
		t.Fatalf("disable policy: %v", err)
	}
	if err := svc.DB.Create(&clusterModels.ReplicationGuestOperation{
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 701,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		State:     clusterModels.ReplicationGuestOperationCutover,
		Token:     "migration:node-a:701", OwnerNodeID: "node-a", TargetNodeID: "node-b", TaskID: 701,
		AcquiredAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("create migration operation: %v", err)
	}
	err := svc.RequireReplicationDatasetMutationAllowed(
		context.Background(), pool+"/sylve/virtual-machines/701",
	)
	if err == nil || !strings.Contains(err.Error(), "replication_protected_dataset_mutation_blocked") {
		t.Fatalf("active migration dataset mutation was allowed: %v", err)
	}
}

func TestReplicationDatasetCreateGuardUsesExactProspectivePath(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	zfstest.EnsureDataset(t, client, pool+"/unrelated")
	svc := newReplicationGuardService(t, pool, client)

	if err := svc.RequireReplicationDatasetCreateAllowed(context.Background(), pool+"/unrelated/child"); err != nil {
		t.Fatalf("unrelated sibling creation was blocked: %v", err)
	}
	err := svc.RequireReplicationDatasetCreateAllowed(
		context.Background(), pool+"/sylve/virtual-machines/701/new-disk",
	)
	if err == nil || !strings.Contains(err.Error(), "replication_protected_dataset_mutation_blocked") {
		t.Fatalf("creation inside protected root was allowed: %v", err)
	}

	err = svc.RequireReplicationDatasetMutationAllowed(context.Background(), pool+"/sylve")
	if err == nil || !strings.Contains(err.Error(), "replication_protected_dataset_mutation_blocked") {
		t.Fatalf("destructive ancestor mutation was allowed: %v", err)
	}
}

func TestReplicationDatasetCreateGuardUsesInheritedStandbyProvenance(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	standby := pool + "/standby"
	zfstest.EnsureDataset(t, client, standby)
	setReplicationGuardZFSProperty(t, standby, "sylve:replication-policy-id", "7")
	svc := newReplicationGuardService(t, pool, client)
	err := svc.RequireReplicationDatasetCreateAllowed(context.Background(), standby+"/child")
	if err == nil || !strings.Contains(err.Error(), "policy_7") {
		t.Fatalf("standby provenance did not block child creation: %v", err)
	}

	err = svc.RequireReplicationDatasetMutationAllowed(context.Background(), standby)
	if err == nil || !strings.Contains(err.Error(), "policy_7") {
		t.Fatalf("recursive real-ZFS provenance did not block ancestor mutation: %v", err)
	}
}

func TestReplicationDatasetGuardProtectsLegacyEnabledFilesystemShare(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	enabledShare := pool + "/shared/enabled"
	disabledShare := pool + "/shared/disabled"
	zfstest.EnsureDataset(t, client, enabledShare)
	zfstest.EnsureDataset(t, client, disabledShare)
	svc := newReplicationGuardService(t, pool, client)

	var vm vmModels.VM
	if err := svc.DB.Where("rid = ?", 701).First(&vm).Error; err != nil {
		t.Fatalf("load guarded VM: %v", err)
	}
	seedFilesystem := func(name string, enabled bool) {
		dataset := vmModels.VMStorageDataset{Pool: pool, Name: name}
		if err := svc.DB.Session(&gorm.Session{SkipHooks: true}).Create(&dataset).Error; err != nil {
			t.Fatalf("seed filesystem dataset %s: %v", name, err)
		}
		storage := vmModels.Storage{
			VMID: vm.ID, Name: "share", Type: vmModels.VMStorageTypeFilesystem,
			Pool: pool, Enable: enabled, DatasetID: &dataset.ID,
		}
		if err := svc.DB.Session(&gorm.Session{SkipHooks: true}).Create(&storage).Error; err != nil {
			t.Fatalf("seed filesystem storage %s: %v", name, err)
		}
	}
	seedFilesystem(enabledShare, true)
	seedFilesystem(disabledShare, false)

	err := svc.RequireReplicationDatasetMutationAllowed(context.Background(), enabledShare)
	if err == nil || !strings.Contains(err.Error(), "replication_protected_dataset_mutation_blocked") {
		t.Fatalf("enabled legacy filesystem share mutation was allowed: %v", err)
	}
	if err := svc.RequireReplicationDatasetMutationAllowed(context.Background(), disabledShare); err != nil {
		t.Fatalf("disabled filesystem share was treated as protected workload data: %v", err)
	}
}

// SPDX-License-Identifier: BSD-2-Clause

package vmModels

import (
	"context"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestReplicationStorageTopologyGuard(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &VM{}, &Storage{}, &clusterModels.ReplicationPolicy{})
	vm := VM{RID: 4101, Name: "protected-vm"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("create vm: %v", err)
	}
	storage := Storage{VMID: vm.ID, Name: "disk-1", Type: VMStorageTypeZVol, Pool: "tank", Enable: true}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatalf("create storage: %v", err)
	}
	dataset := VMStorageDataset{Pool: "tank", Name: "tank/sylve/virtual-machines/4101/zvol-1", GUID: "guid-4101"}
	if err := db.Create(&dataset).Error; err != nil {
		t.Fatalf("create storage dataset: %v", err)
	}
	if err := db.Model(&storage).Update("dataset_id", dataset.ID).Error; err != nil {
		t.Fatalf("attach storage dataset: %v", err)
	}
	policy := clusterModels.ReplicationPolicy{
		Name:            "vm-policy",
		GuestType:       clusterModels.ReplicationGuestTypeVM,
		GuestID:         vm.RID,
		Enabled:         true,
		TransitionState: clusterModels.ReplicationTransitionStateCompleted,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}

	var unchanged Storage
	if err := db.First(&unchanged, storage.ID).Error; err != nil {
		t.Fatalf("reload storage: %v", err)
	}
	if err := db.Save(&unchanged).Error; err != nil {
		t.Fatalf("unchanged association save should be allowed: %v", err)
	}

	unchanged.Name = "disk-renamed"
	if err := db.Save(&unchanged).Error; err == nil || !strings.Contains(err.Error(), "requires_policy_disabled") {
		t.Fatalf("protected storage update was not blocked: %v", err)
	}
	newStorage := Storage{VMID: vm.ID, Name: "disk-2", Type: VMStorageTypeZVol, Pool: "tank", Enable: true}
	if err := db.Create(&newStorage).Error; err == nil || !strings.Contains(err.Error(), "requires_policy_disabled") {
		t.Fatalf("protected storage create was not blocked: %v", err)
	}
	newShare := Storage{VMID: vm.ID, Name: "share", Type: VMStorageTypeFilesystem, Pool: "tank", Enable: true}
	if err := db.Create(&newShare).Error; err == nil || !strings.Contains(err.Error(), ReplicationFilesystemStorageUnsupported) {
		t.Fatalf("protected filesystem attach did not return the stable eligibility error: %v", err)
	}
	if err := db.Delete(&Storage{}, storage.ID).Error; err == nil || !strings.Contains(err.Error(), "requires_policy_disabled") {
		t.Fatalf("protected storage delete was not blocked: %v", err)
	}
	if err := db.Model(&Storage{}).Where("id = ?", storage.ID).Update("name", "bulk-renamed").Error; err == nil {
		t.Fatal("protected bulk storage update bypassed topology guard")
	}
	dataset.Name = "tank/moved"
	if err := db.Save(&dataset).Error; err == nil {
		t.Fatal("protected VM storage-dataset update bypassed topology guard")
	}
	if err := db.Delete(&VMStorageDataset{}, dataset.ID).Error; err == nil {
		t.Fatal("protected VM storage-dataset delete bypassed topology guard")
	}

	if err := db.Model(&policy).Updates(map[string]any{
		"transition_state":  clusterModels.ReplicationTransitionStatePromoting,
		"transition_run_id": "run-4101",
	}).Error; err != nil {
		t.Fatalf("begin transition: %v", err)
	}
	if err := db.WithContext(clusterModels.WithReplicationTransitionAuthority(context.Background(), "wrong-run", policy.OwnerEpoch)).
		Save(&unchanged).Error; err == nil {
		t.Fatal("mismatched transition run bypassed topology guard")
	}
	if err := db.WithContext(clusterModels.WithReplicationTransitionAuthority(context.Background(), "run-4101", policy.OwnerEpoch+1)).
		Save(&unchanged).Error; err == nil {
		t.Fatal("mismatched transition epoch bypassed topology guard")
	}
	if err := db.WithContext(clusterModels.WithReplicationTransitionAuthority(context.Background(), "run-4101", policy.OwnerEpoch)).
		Save(&unchanged).Error; err != nil {
		t.Fatalf("exact transition reconciliation was blocked: %v", err)
	}
}

func TestReplicationISOStorageRequiresPolicyDisabled(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &VM{}, &Storage{}, &clusterModels.ReplicationPolicy{})
	vm := VM{RID: 4201, Name: "protected-iso-vm"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("create vm: %v", err)
	}
	iso := Storage{
		VMID:         vm.ID,
		Name:         "installer",
		Type:         VMStorageTypeDiskImage,
		DownloadUUID: "iso-4201",
		Pool:         "legacy-stale-pool",
		Emulation:    AHCICDStorageEmulation,
		Enable:       true,
	}
	if err := db.Create(&iso).Error; err != nil {
		t.Fatalf("create ISO before protection: %v", err)
	}
	policy := clusterModels.ReplicationPolicy{
		Name:            "vm-iso-policy",
		GuestType:       clusterModels.ReplicationGuestTypeVM,
		GuestID:         vm.RID,
		Enabled:         true,
		TransitionState: clusterModels.ReplicationTransitionStateCompleted,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}
	if err := db.Delete(&iso).Error; err == nil || !strings.Contains(err.Error(), "requires_policy_disabled") {
		t.Fatalf("protected ISO delete bypassed topology guard: %v", err)
	}
	iso.Name = "renamed-installer"
	if err := db.Save(&iso).Error; err == nil || !strings.Contains(err.Error(), "requires_policy_disabled") {
		t.Fatalf("protected ISO update bypassed topology guard: %v", err)
	}
	if err := db.Model(&policy).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable policy: %v", err)
	}
	if err := db.Save(&iso).Error; err != nil {
		t.Fatalf("ISO update remained blocked after policy disable: %v", err)
	}
}

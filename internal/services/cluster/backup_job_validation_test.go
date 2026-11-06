// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"context"
	"fmt"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

func TestValidateBackupJobSafetyLocalReturnsBoundReceipt(t *testing.T) {
	service := newManagedDatasetGuardTestService(t)
	service.NodeID = "node-runner"
	seedManagedDatasetGuardGuests(t, service)

	result, err := service.ValidateBackupJobSafetyLocal(context.Background(), BackupJobSafetyValidationRequest{
		ExpectedNodeID: "node-runner",
		Mode:           clusterModels.BackupJobModeVM,
		SourceDataset:  " /fast//sylve/virtual-machines/200/ ",
		Recursive:      true,
	})
	if err != nil {
		t.Fatalf("validate VM: %v", err)
	}
	if !result.Valid || result.NodeID != "node-runner" || result.GuestType != "vm" || result.GuestID != 200 {
		t.Fatalf("unexpected validation result: %+v", result)
	}
	if result.SourceDataset != "fast/sylve/virtual-machines/200" ||
		result.Classification != BackupJobSourceClassificationManagedVM ||
		!result.ManagedScope || result.FriendlySource != "guard-vm" {
		t.Fatalf("unexpected canonical receipt: %+v", result)
	}
	if result.PlacementFence == nil || result.PlacementFence.RunnerNodeID != "node-runner" {
		t.Fatalf("placement fence = %+v", result.PlacementFence)
	}

	mismatch, err := service.ValidateBackupJobSafetyLocal(context.Background(), BackupJobSafetyValidationRequest{
		ExpectedNodeID: "node-other", Mode: "dataset", SourceDataset: "tank/data",
	})
	if err != nil {
		t.Fatalf("identity mismatch request: %v", err)
	}
	if mismatch.Valid || mismatch.NodeID != "node-runner" ||
		!strings.Contains(mismatch.ValidationError, "backup_runner_identity_mismatch") {
		t.Fatalf("identity mismatch result: %+v", mismatch)
	}
}

func TestValidateBackupJobOnRunnerCanSkipDisabledTargetConnectivity(t *testing.T) {
	service := newManagedDatasetGuardTestService(t)
	service.NodeID = "node-runner"
	seedManagedDatasetGuardGuests(t, service)
	job := &clusterModels.BackupJob{
		Name: "disabled-vm-backup", TargetID: 999, RunnerNodeID: "node-runner",
		Mode: clusterModels.BackupJobModeVM, SourceDataset: "fast/sylve/virtual-machines/200",
		Recursive: true, Enabled: false,
	}

	result, _, err := service.validateBackupJobOnRunnerWithTarget(
		context.Background(), job, BackupJobPlacementAuthorization{}, false,
	)
	if err != nil {
		t.Fatalf("validate disabled job without target connectivity: %v", err)
	}
	if !result.Valid || result.TargetReadiness != nil {
		t.Fatalf("unexpected disabled-job validation receipt: %+v", result)
	}

	_, _, err = service.validateBackupJobOnRunner(
		context.Background(), job, BackupJobPlacementAuthorization{},
	)
	if err == nil || !strings.Contains(err.Error(), "backup_target_not_found") {
		t.Fatalf("enabled target validation error = %v", err)
	}
}

func TestValidateBackupJobSafetyLocalUsesRunnerManagedInventory(t *testing.T) {
	service := newManagedDatasetGuardTestService(t)
	service.NodeID = "node-runner"
	seedManagedDatasetGuardGuests(t, service)

	result, err := service.ValidateBackupJobSafetyLocal(context.Background(), BackupJobSafetyValidationRequest{
		ExpectedNodeID: "node-runner",
		Mode:           clusterModels.BackupJobModeDataset,
		SourceDataset:  "fast/sylve/virtual-machines/200/zvol-1",
	})
	if err != nil {
		t.Fatalf("validate managed dataset: %v", err)
	}
	if result.Valid || !result.ManagedScope || result.Classification != BackupJobSourceClassificationManagedScope ||
		!strings.Contains(result.ValidationError, "dataset_backup_source_contains_managed_guest") {
		t.Fatalf("managed dataset result: %+v", result)
	}
}

func seedRemoteValidationVM(t *testing.T, service *Service, rid uint, pool, name string) {
	t.Helper()
	vm := vmModels.VM{RID: rid, Name: name}
	if err := service.DB.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	dataset := vmModels.VMStorageDataset{
		Pool: pool, Name: fmt.Sprintf("%s/sylve/virtual-machines/%d/disk0", pool, rid),
		GUID: fmt.Sprintf("vm-%d-guid", rid),
	}
	if err := service.DB.Create(&dataset).Error; err != nil {
		t.Fatalf("seed VM dataset: %v", err)
	}
	if err := service.DB.Create(&vmModels.Storage{
		VMID: vm.ID, Type: vmModels.VMStorageTypeZVol, Pool: pool, Enable: true, DatasetID: &dataset.ID,
	}).Error; err != nil {
		t.Fatalf("seed VM storage: %v", err)
	}
}

func TestBackupJobSafetyReceiptIsBoundToTheRunner(t *testing.T) {
	request := BackupJobSafetyValidationRequest{
		ExpectedNodeID: "node-runner",
		Mode:           clusterModels.BackupJobModeDataset,
		SourceDataset:  "tank/data",
	}
	result := BackupJobSafetyValidationResult{
		NodeID: "node-runner", Valid: true,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data",
		Classification: BackupJobSourceClassificationDataset,
		FriendlySource: "tank/data",
	}
	if err := validateBackupJobSafetyReceipt(request, result); err != nil {
		t.Fatalf("validate receipt: %v", err)
	}
	result.NodeID = "spoofed-node"
	if err := validateBackupJobSafetyReceipt(request, result); err == nil ||
		!strings.Contains(err.Error(), "backup_runner_identity_mismatch") {
		t.Fatalf("spoofed receipt error = %v", err)
	}
}

func TestFetchBackupJobSafetyValidationFailsClosedForAnUnknownRunner(t *testing.T) {
	service := &Service{NodeID: "node-leader", AuthService: clusterAuthStub{}}
	_, err := service.fetchBackupJobSafetyValidation(t.Context(), "node-offline", BackupJobSafetyValidationRequest{
		ExpectedNodeID: "node-offline", Mode: "dataset", SourceDataset: "tank/data",
	})
	if err == nil || !strings.Contains(err.Error(), "backup_runner_validation_request_failed") {
		t.Fatalf("unknown runner error = %v", err)
	}
}

func TestAuthorizeBackupJobPlacementRequiresExactTransitionIdentity(t *testing.T) {
	staleRunner := &clusterModels.BackupJobPlacementFence{
		GuestType: "vm", GuestID: 8, RunnerNodeID: "node-old",
		PolicyPresent: true, PolicyEnabled: true, PolicyActiveNodeID: "node-new", PolicyOwnerEpoch: 2,
	}
	if err := authorizeBackupJobPlacement(staleRunner, BackupJobPlacementAuthorization{}, false); err != nil {
		t.Fatalf("previous stale runner should remain repairable: %v", err)
	}
	if err := authorizeBackupJobPlacement(staleRunner, BackupJobPlacementAuthorization{}, true); err == nil ||
		!strings.Contains(err.Error(), "backup_runner_not_guest_owner") {
		t.Fatalf("new stale runner placement error = %v", err)
	}

	fence := &clusterModels.BackupJobPlacementFence{
		GuestType: "vm", GuestID: 9, RunnerNodeID: "node-b",
		PolicyPresent: true, PolicyEnabled: true, PolicyActiveNodeID: "node-b", PolicyOwnerEpoch: 3,
		PolicyTransitionState: clusterModels.ReplicationTransitionStatePromoting,
		PolicyTransitionRunID: "run-3",
		OperationPresent:      true, Operation: clusterModels.ReplicationGuestOperationMigration,
		OperationToken: "migration-token",
	}
	if err := authorizeBackupJobPlacement(fence, BackupJobPlacementAuthorization{}, true); err == nil {
		t.Fatal("zero authorization unexpectedly accepted")
	}
	if err := authorizeBackupJobPlacement(fence, BackupJobPlacementAuthorization{
		GuestOperationToken: "migration-token", TransitionRunID: "run-3",
	}, true); err != nil {
		t.Fatalf("exact authorization rejected: %v", err)
	}
	if err := authorizeBackupJobPlacement(fence, BackupJobPlacementAuthorization{
		GuestOperationToken: "wrong", TransitionRunID: "run-3",
	}, true); err == nil {
		t.Fatal("wrong operation token unexpectedly accepted")
	}
}

// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
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

func registerBackupJobValidationPeer(t *testing.T, sim *clusterPeerSimulator, service *Service) {
	t.Helper()
	sim.serveMux.HandleFunc("/api/intra-cluster/backup-job-safety-validation", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request BackupJobSafetyValidationRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := service.ValidateBackupJobSafetyLocal(r.Context(), request)
		if err != nil {
			_ = json.NewEncoder(w).Encode(internal.APIResponse[any]{
				Status: "error", Message: "backup_runner_validation_failed", Error: err.Error(),
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(internal.APIResponse[BackupJobSafetyValidationResult]{
			Status: "success", Data: result,
		})
	})
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

func TestFetchBackupJobSafetyValidationAuthenticatesAndBindsReceipt(t *testing.T) {
	request := BackupJobSafetyValidationRequest{
		ExpectedNodeID:          "node-runner",
		MinimumRaftAppliedIndex: 7,
		Mode:                    clusterModels.BackupJobModeDataset,
		SourceDataset:           "tank/data",
	}
	result := BackupJobSafetyValidationResult{
		NodeID: "node-runner", RaftAppliedIndex: 7, Valid: true,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data",
		Classification: BackupJobSourceClassificationDataset,
		FriendlySource: "tank/data",
	}

	sim := newClusterPeerSimulator()
	defer sim.Close()
	sim.serveMux.HandleFunc("/api/intra-cluster/backup-job-safety-validation", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(internal.APIResponse[BackupJobSafetyValidationResult]{
			Status: "success", Data: result,
		})
	})
	service := &Service{NodeID: "node-leader", AuthService: clusterAuthStub{}}

	got, err := service.fetchBackupJobSafetyValidation(t.Context(), "node-runner", sim.Addr(), request)
	if err != nil {
		t.Fatalf("fetch validation: %v", err)
	}
	if err := validateBackupJobSafetyReceipt(request, got); err != nil {
		t.Fatalf("validate receipt: %v", err)
	}
	captured := sim.FindRequest("/api/intra-cluster/backup-job-safety-validation")
	if captured == nil || captured.Header.Get("X-Cluster-Token") != "Bearer test-cluster-token" {
		t.Fatalf("authenticated request not observed: %+v", captured)
	}

	result.NodeID = "spoofed-node"
	got, err = service.fetchBackupJobSafetyValidation(t.Context(), "node-runner", sim.Addr(), request)
	if err != nil {
		t.Fatalf("fetch spoofed receipt: %v", err)
	}
	if err := validateBackupJobSafetyReceipt(request, got); err == nil ||
		!strings.Contains(err.Error(), "backup_runner_identity_mismatch") {
		t.Fatalf("spoofed receipt error = %v", err)
	}
}

func TestFetchBackupJobSafetyValidationFailsClosedWhenRunnerOffline(t *testing.T) {
	service := &Service{NodeID: "node-leader", AuthService: clusterAuthStub{}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := service.fetchBackupJobSafetyValidation(
		ctx,
		"node-offline",
		"127.0.0.1:1",
		BackupJobSafetyValidationRequest{
			ExpectedNodeID: "node-offline", Mode: "dataset", SourceDataset: "tank/data",
		},
	)
	if err == nil || !strings.Contains(err.Error(), "backup_runner_validation_request_failed") {
		t.Fatalf("offline runner error = %v", err)
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

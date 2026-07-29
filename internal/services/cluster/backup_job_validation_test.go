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
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/hashicorp/raft"
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

func seedRemoteValidationJail(t *testing.T, service *Service, ctid uint, pool, name string) {
	t.Helper()
	jail := jailModels.Jail{CTID: ctid, Name: name, Type: jailModels.JailTypeFreeBSD}
	if err := service.DB.Create(&jail).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}
	if err := service.DB.Create(&jailModels.Storage{
		JailID: jail.ID, Pool: pool, GUID: fmt.Sprintf("jail-%d-guid", ctid),
		Name: "Base Filesystem", IsBase: true,
	}).Error; err != nil {
		t.Fatalf("seed jail storage: %v", err)
	}
}

func TestRemoteRunnerValidationCreatesGuestAndDatasetJobsAcrossThreeNodes(t *testing.T) {
	models := []any{
		&clusterModels.BackupTarget{}, &clusterModels.BackupJob{},
		&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationGuestOperation{},
		&vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{},
		&jailModels.Jail{}, &jailModels.Storage{},
	}
	nodes := setupClusterRaftTestNodes(t, 3, models...)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	runner := remoteClusterRaftTestNode(t, nodes, leader)
	leader.service.NodeID = leader.id
	runner.service.NodeID = runner.id
	leader.service.AuthService = clusterAuthStub{}

	for _, node := range nodes {
		if err := node.service.DB.Create(&clusterModels.BackupTarget{
			ID: 1, Name: "target", SSHHost: "backup", BackupRoot: "tank/backups", Enabled: true,
		}).Error; err != nil {
			t.Fatalf("seed target on %s: %v", node.id, err)
		}
	}
	seedRemoteValidationVM(t, runner.service, 401, "fast", "remote-vm")
	seedRemoteValidationJail(t, runner.service, 501, "jpool", "remote-jail")

	sim := newClusterPeerSimulator()
	defer sim.Close()
	registerBackupJobValidationPeer(t, sim, runner.service)
	leader.service.backupJobValidationAPIForNode = func(nodeID string, _ raft.ServerAddress) (string, error) {
		if nodeID != runner.id {
			return "", fmt.Errorf("unexpected runner %s", nodeID)
		}
		return sim.Addr(), nil
	}

	enabled := true
	requests := []clusterServiceInterfaces.BackupJobReq{
		{
			Name: "remote-vm-job", TargetID: 1, RunnerNodeID: runner.id,
			Mode: clusterModels.BackupJobModeVM, SourceDataset: "fast/sylve/virtual-machines/401",
			Recursive: true, CronExpr: "0 0 * * *", Enabled: &enabled,
		},
		{
			Name: "remote-jail-job", TargetID: 1, RunnerNodeID: runner.id,
			Mode: clusterModels.BackupJobModeJail, JailRootDataset: "jpool/sylve/jails/501",
			CronExpr: "0 1 * * *", Enabled: &enabled,
		},
		{
			Name: "remote-dataset-job", TargetID: 1, RunnerNodeID: runner.id,
			Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/ordinary",
			CronExpr: "0 2 * * *", Enabled: &enabled,
		},
	}
	for _, request := range requests {
		if err := leader.service.ProposeBackupJobCreateContext(context.Background(), request, false); err != nil {
			t.Fatalf("create %s: %v", request.Name, err)
		}
	}

	waitForClusterCondition(t, 5*time.Second, "backup job replication", func() bool {
		for _, node := range nodes {
			var count int64
			if node.service.DB.Model(&clusterModels.BackupJob{}).
				Where("runner_node_id = ?", runner.id).Count(&count).Error != nil || count != 3 {
				return false
			}
		}
		return true
	})
	var job clusterModels.BackupJob
	if err := leader.service.DB.Where("name = ?", "remote-vm-job").First(&job).Error; err != nil {
		t.Fatalf("load job: %v", err)
	}
	if job.FriendlySrc != "remote-vm" || job.RunnerNodeID != runner.id {
		t.Fatalf("stored job = %+v", job)
	}
	if err := leader.service.ProposeBackupJobUpdateContext(
		context.Background(),
		job.ID,
		clusterServiceInterfaces.BackupJobReq{
			Name: "remote-vm-job-updated", TargetID: 1, RunnerNodeID: runner.id,
			Mode: clusterModels.BackupJobModeVM, SourceDataset: "fast/sylve/virtual-machines/401",
			Recursive: true, CronExpr: "0 3 * * *", Enabled: &enabled,
		},
		false,
		BackupJobPlacementAuthorization{},
	); err != nil {
		t.Fatalf("update remote VM job: %v", err)
	}
	waitForClusterCondition(t, 5*time.Second, "backup job update replication", func() bool {
		for _, node := range nodes {
			var updated clusterModels.BackupJob
			if node.service.DB.First(&updated, job.ID).Error != nil || updated.Name != "remote-vm-job-updated" {
				return false
			}
		}
		return true
	})

	request := sim.FindRequest("/api/intra-cluster/backup-job-safety-validation")
	if request == nil || request.Header.Get("X-Cluster-Token") != "Bearer test-cluster-token" {
		t.Fatalf("authenticated validation request not observed: %+v", request)
	}
}

func TestRemoteRunnerValidationRejectsManagedDatasetAndStaleHealthRunner(t *testing.T) {
	models := []any{
		&clusterModels.BackupTarget{}, &clusterModels.BackupJob{}, &clusterModels.ClusterNode{},
		&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationGuestOperation{},
		&vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{},
		&jailModels.Jail{}, &jailModels.Storage{},
	}
	nodes := setupClusterRaftTestNodes(t, 2, models...)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	runner := remoteClusterRaftTestNode(t, nodes, leader)
	leader.service.NodeID = leader.id
	runner.service.NodeID = runner.id
	leader.service.AuthService = clusterAuthStub{}
	if err := leader.service.DB.Create(&clusterModels.BackupTarget{
		ID: 1, Name: "target", SSHHost: "backup", BackupRoot: "tank/backups", Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	seedRemoteValidationVM(t, runner.service, 402, "fast", "managed-vm")

	sim := newClusterPeerSimulator()
	defer sim.Close()
	registerBackupJobValidationPeer(t, sim, runner.service)
	leader.service.backupJobValidationAPIForNode = func(string, raft.ServerAddress) (string, error) {
		return sim.Addr(), nil
	}
	enabled := true
	err := leader.service.ProposeBackupJobCreateContext(context.Background(), clusterServiceInterfaces.BackupJobReq{
		Name: "unsafe-dataset", TargetID: 1, RunnerNodeID: runner.id,
		Mode:          clusterModels.BackupJobModeDataset,
		SourceDataset: "fast/sylve/virtual-machines/402/disk0", Enabled: &enabled,
	}, false)
	if err == nil || !strings.Contains(err.Error(), "dataset_backup_source_contains_managed_guest") {
		t.Fatalf("managed dataset error = %v", err)
	}

	if err := leader.service.DB.Create(&clusterModels.ClusterNode{
		NodeUUID: "removed-node", API: "192.0.2.10:8184", Status: "online",
	}).Error; err != nil {
		t.Fatalf("seed stale health row: %v", err)
	}
	err = leader.service.ProposeBackupJobCreateContext(context.Background(), clusterServiceInterfaces.BackupJobReq{
		Name: "stale-runner", TargetID: 1, RunnerNodeID: "removed-node",
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data", Enabled: &enabled,
	}, false)
	if err == nil || !strings.Contains(err.Error(), "backup_runner_not_raft_member") {
		t.Fatalf("stale runner error = %v", err)
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

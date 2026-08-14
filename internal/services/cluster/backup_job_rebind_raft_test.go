// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/hashicorp/raft"
)

func applyBackupJobRebindTestLeaderCommand(
	t *testing.T,
	nodes []*clusterRaftTestNode,
	action func(*clusterRaftTestNode) error,
) *clusterRaftTestNode {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		leader := findClusterRaftLeader(nodes)
		if leader == nil {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if err := action(leader); err == nil {
			return leader
		} else {
			lastErr = err
			lower := strings.ToLower(err.Error())
			if !strings.Contains(lower, "not_leader") && !strings.Contains(lower, "not leader") &&
				!strings.Contains(lower, "leadership") {
				t.Fatalf("leader command failed: %v", err)
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("leader command did not converge: %v", lastErr)
	return nil
}

func TestIntegrationRaftBackupJobRebindSurvivesLeadershipChange(t *testing.T) {
	models := []any{
		&clusterModels.BackupTarget{}, &clusterModels.BackupJob{},
		&clusterModels.BackupJobRunnerRebind{}, &clusterModels.BackupJobRunnerRebindItem{},
		&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{}, &clusterModels.ReplicationGuestOperation{},
		&clusterModels.ReplicationGuestOperationReceipt{},
		&vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{},
	}
	nodes := setupClusterRaftTestNodes(t, 3, models...)
	leaderC := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	remotes := make([]*clusterRaftTestNode, 0, 2)
	for _, node := range nodes {
		node.service.NodeID = node.id
		if node != leaderC {
			remotes = append(remotes, node)
		}
	}
	if len(remotes) != 2 {
		t.Fatalf("remote nodes = %d", len(remotes))
	}
	sourceA, targetB := remotes[0], remotes[1]

	for _, node := range nodes {
		if err := node.service.DB.Create(&clusterModels.BackupTarget{
			ID: 1, Name: "target", SSHHost: "backup", BackupRoot: "tank/backups", Enabled: true,
		}).Error; err != nil {
			t.Fatalf("seed target on %s: %v", node.id, err)
		}
		jobs := []clusterModels.BackupJob{
			{
				ID: 101, Name: "valid", TargetID: 1, RunnerNodeID: sourceA.id,
				Mode: clusterModels.BackupJobModeVM, SourceDataset: "fast/sylve/virtual-machines/707",
				DestSuffix: "virtual-machines/707/j-2t/active", Recursive: true,
				CronExpr: "0 0 * * *", Enabled: true,
			},
			{
				ID: 102, Name: "legacy-invalid", TargetID: 1, RunnerNodeID: sourceA.id,
				Mode: clusterModels.BackupJobModeVM, SourceDataset: "fast/sylve/virtual-machines/707",
				DestSuffix: "virtual-machines/707/j-2u/active", Recursive: false,
				CronExpr: "0 1 * * *", Enabled: true,
			},
		}
		if err := node.service.DB.Create(&jobs).Error; err != nil {
			t.Fatalf("seed jobs on %s: %v", node.id, err)
		}
	}
	seedRemoteValidationVM(t, sourceA.service, 707, "fast", "source-vm")
	seedRemoteValidationVM(t, targetB.service, 707, "fast", "migrated-vm")
	preflightSim := newClusterPeerSimulator()
	registerBackupJobValidationPeer(t, preflightSim, sourceA.service)
	for _, node := range nodes {
		node.service.AuthService = clusterAuthStub{}
		node.service.backupJobValidationAPIForNode = func(nodeID string, _ raft.ServerAddress) (string, error) {
			if nodeID != sourceA.id {
				return "", fmt.Errorf("unexpected preflight runner %s", nodeID)
			}
			return preflightSim.Addr(), nil
		}
	}

	token := fmt.Sprintf("migration:%s:%d", sourceA.id, 7007)
	leaderC = applyBackupJobRebindTestLeaderCommand(t, nodes, func(leader *clusterRaftTestNode) error {
		return leader.service.AcquireReplicationGuestOperation(clusterModels.ReplicationGuestOperationAcquire{
			GuestType: clusterModels.BackupJobModeVM, GuestID: 707,
			Operation: clusterModels.ReplicationGuestOperationMigration, Token: token,
			OwnerNodeID: sourceA.id, TargetNodeID: targetB.id, TaskID: 7007,
		}, false)
	})
	leaderC = applyBackupJobRebindTestLeaderCommand(t, nodes, func(leader *clusterRaftTestNode) error {
		return leader.service.PrepareBackupJobRunnerRebind(
			context.Background(), clusterModels.BackupJobModeVM, 707, targetB.id, token, false,
		)
	})
	var plannedValid, plannedInvalid clusterModels.BackupJobRunnerRebindItem
	if err := leaderC.service.DB.Where("operation_token = ? AND job_id = ?", token, 101).
		First(&plannedValid).Error; err != nil {
		t.Fatal(err)
	}
	if err := leaderC.service.DB.Where("operation_token = ? AND job_id = ?", token, 102).
		First(&plannedInvalid).Error; err != nil {
		t.Fatal(err)
	}
	if plannedValid.State != clusterModels.BackupJobRunnerRebindItemPending || plannedValid.Error != "" {
		t.Fatalf("valid source job failed preflight: %+v", plannedValid)
	}
	if plannedInvalid.State != clusterModels.BackupJobRunnerRebindItemPending ||
		!strings.Contains(plannedInvalid.Error, "vm_backup_requires_recursive") {
		t.Fatalf("invalid legacy job was not classified before cutover: %+v", plannedInvalid)
	}
	preflightSim.Close()
	leaderC = applyBackupJobRebindTestLeaderCommand(t, nodes, func(leader *clusterRaftTestNode) error {
		return leader.service.SealReplicationGuestOperation(clusterModels.ReplicationGuestOperationTransition{
			GuestType: clusterModels.BackupJobModeVM, GuestID: 707,
			Operation: clusterModels.ReplicationGuestOperationMigration, Token: token,
		}, false)
	})
	leaderC = applyBackupJobRebindTestLeaderCommand(t, nodes, func(leader *clusterRaftTestNode) error {
		return leader.service.ReadyBackupJobRunnerRebind(token, false)
	})

	// The replicated plan was prepared and marked ready by leader C. Move
	// leadership to source A before any item is applied.
	waitForClusterCondition(t, 8*time.Second, "leadership transfer", func() bool {
		if sourceA.raft.State() == raft.Leader {
			return true
		}
		current := findClusterRaftLeader(nodes)
		if current != nil {
			_ = current.raft.LeadershipTransferToServer(raft.ServerID(sourceA.id), sourceA.addr).Error()
		}
		return false
	})
	waitForClusterCondition(t, 8*time.Second, "ready plan on new leader", func() bool {
		var operation clusterModels.BackupJobRunnerRebind
		return sourceA.service.DB.Where("token = ?", token).First(&operation).Error == nil &&
			operation.State == clusterModels.BackupJobRunnerRebindStateReady
	})
	newLeader := sourceA
	newLeader.service.AuthService = clusterAuthStub{}
	newLeader.service.backupJobValidationAPIForNode = func(nodeID string, _ raft.ServerAddress) (string, error) {
		if nodeID != targetB.id {
			return "", fmt.Errorf("unexpected runner %s", nodeID)
		}
		return "127.0.0.1:1", nil
	}
	if err := newLeader.service.ReconcileBackupJobRunnerRebind(context.Background(), token); err == nil ||
		!strings.Contains(err.Error(), "backup_job_runner_rebind_pending") {
		t.Fatalf("transient runner failure did not remain pending: %v", err)
	}
	var pendingOperation clusterModels.BackupJobRunnerRebind
	if err := newLeader.service.DB.Where("token = ?", token).First(&pendingOperation).Error; err != nil {
		t.Fatal(err)
	}
	if pendingOperation.State != clusterModels.BackupJobRunnerRebindStateReady {
		t.Fatalf("transient failure closed plan: %+v", pendingOperation)
	}

	sim := newClusterPeerSimulator()
	defer sim.Close()
	registerBackupJobValidationPeer(t, sim, targetB.service)
	newLeader.service.backupJobValidationAPIForNode = func(nodeID string, _ raft.ServerAddress) (string, error) {
		if nodeID != targetB.id {
			return "", fmt.Errorf("unexpected runner %s", nodeID)
		}
		return sim.Addr(), nil
	}

	if err := newLeader.service.ReconcileBackupJobRunnerRebind(context.Background(), token); err != nil {
		t.Fatalf("reconcile rebind after leadership change: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "rebind replication", func() bool {
		for _, node := range nodes {
			var operation clusterModels.BackupJobRunnerRebind
			if node.service.DB.Where("token = ?", token).First(&operation).Error != nil ||
				operation.State != clusterModels.BackupJobRunnerRebindStateCompletedWithRepairs {
				return false
			}
			var valid, invalid clusterModels.BackupJob
			if node.service.DB.First(&valid, 101).Error != nil || valid.RunnerNodeID != targetB.id ||
				valid.FriendlySrc != "migrated-vm" || !valid.Enabled {
				return false
			}
			if node.service.DB.First(&invalid, 102).Error != nil || invalid.RunnerNodeID != targetB.id ||
				invalid.Enabled || invalid.LastStatus != clusterModels.BackupJobRunnerRebindItemRepairRequired ||
				!strings.Contains(invalid.LastError, "vm_backup_requires_recursive") {
				return false
			}
		}
		return true
	})

	var items []clusterModels.BackupJobRunnerRebindItem
	if err := newLeader.service.DB.Where("operation_token = ?", token).Order("job_id ASC").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].State != clusterModels.BackupJobRunnerRebindItemRebound ||
		items[1].State != clusterModels.BackupJobRunnerRebindItemRepairRequired {
		t.Fatalf("rebind items = %+v", items)
	}

	// Reconciliation is exact-token idempotent after completion.
	if err := newLeader.service.ReconcileBackupJobRunnerRebind(context.Background(), token); err != nil {
		t.Fatalf("replay completed reconciliation: %v", err)
	}
	if err := newLeader.service.CompleteReplicationGuestOperation(clusterModels.ReplicationGuestOperationTransition{
		GuestType: clusterModels.BackupJobModeVM, GuestID: 707,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		Token:     token, TargetNodeID: targetB.id,
	}, false); err != nil {
		t.Fatalf("complete migration operation: %v", err)
	}

	enabled := true
	if err := newLeader.service.ProposeBackupJobUpdateContext(
		context.Background(),
		102,
		clusterServiceInterfaces.BackupJobReq{
			Name: "legacy-invalid", TargetID: 1, RunnerNodeID: targetB.id,
			Mode: clusterModels.BackupJobModeVM, SourceDataset: "fast/sylve/virtual-machines/707",
			Recursive: true, CronExpr: "0 1 * * *", Enabled: &enabled,
		},
		false,
		BackupJobPlacementAuthorization{},
	); err != nil {
		t.Fatalf("repair job through normal validated update: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "repair acknowledgement replication", func() bool {
		for _, node := range nodes {
			var operation clusterModels.BackupJobRunnerRebind
			if node.service.DB.Where("token = ?", token).First(&operation).Error != nil ||
				operation.State != clusterModels.BackupJobRunnerRebindStateCompleted {
				return false
			}
			var repairedJob clusterModels.BackupJob
			if node.service.DB.First(&repairedJob, 102).Error != nil || !repairedJob.Enabled ||
				repairedJob.LastStatus != "" || repairedJob.LastError != "" {
				return false
			}
			var repairedItem clusterModels.BackupJobRunnerRebindItem
			if node.service.DB.Where("operation_token = ? AND job_id = ?", token, 102).
				First(&repairedItem).Error != nil || repairedItem.State != clusterModels.BackupJobRunnerRebindItemRepaired {
				return false
			}
		}
		return true
	})
}

func TestIntegrationRaftFailoverBackupJobRebindReconcilesOnNewLeader(t *testing.T) {
	models := []any{
		&clusterModels.BackupTarget{}, &clusterModels.BackupJob{}, &clusterModels.BackupJobOperation{},
		&clusterModels.BackupJobRunnerRebind{}, &clusterModels.BackupJobRunnerRebindItem{},
		&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{}, &clusterModels.ReplicationGuestOperation{},
		&clusterModels.ReplicationGuestOperationReceipt{},
		&vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{},
	}
	nodes := setupClusterRaftTestNodes(t, 3, models...)
	leaderC := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	remotes := make([]*clusterRaftTestNode, 0, 2)
	for _, node := range nodes {
		node.service.NodeID = node.id
		if node != leaderC {
			remotes = append(remotes, node)
		}
	}
	sourceA, targetB := remotes[0], remotes[1]
	runID := "failover-808-exact-run"
	requestedAt := clusterModels.NormalizeCommandTime(time.Now().UTC())
	originalRunning := true
	seedRemoteValidationVM(t, sourceA.service, 808, "fast", "source-vm")
	seedRemoteValidationVM(t, targetB.service, 808, "fast", "failed-over-vm")
	for _, node := range nodes {
		if err := node.service.DB.Create(&clusterModels.BackupTarget{
			ID: 1, Name: "target", SSHHost: "backup", BackupRoot: "tank/backups", Enabled: true,
		}).Error; err != nil {
			t.Fatalf("seed target on %s: %v", node.id, err)
		}
		policy := clusterModels.ReplicationPolicy{
			ID: 808, Name: "failover", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 808,
			SourceNodeID: sourceA.id, ActiveNodeID: sourceA.id, OwnerEpoch: 1,
			SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
			FailbackMode: clusterModels.ReplicationFailbackManual,
			FailoverMode: clusterModels.ReplicationFailoverManual,
			CronExpr:     "0 * * * *", Enabled: true,
			ProtectionState: clusterModels.ReplicationProtectionStateSuspended,
			TransitionState: clusterModels.ReplicationTransitionStateDemoting,
			TransitionRunID: runID, TransitionReason: "manual_failover",
			TransitionSourceNodeID: sourceA.id, TransitionTargetNodeID: targetB.id,
			TransitionOwnerEpoch: 1, TransitionRequestedAt: &requestedAt,
			TransitionTriggerValidationRun: true, TransitionOriginalRunning: &originalRunning,
			TransitionOriginalSourceNodeID: sourceA.id,
		}
		if err := node.service.DB.Create(&policy).Error; err != nil {
			t.Fatalf("seed policy on %s: %v", node.id, err)
		}
		if err := node.service.DB.Create(&clusterModels.ReplicationPolicyTarget{
			PolicyID: policy.ID, NodeID: targetB.id, Weight: 100, Ready: true,
		}).Error; err != nil {
			t.Fatalf("seed policy target on %s: %v", node.id, err)
		}
		if err := node.service.DB.Create(&clusterModels.ReplicationLease{
			PolicyID: policy.ID, GuestType: policy.GuestType, GuestID: policy.GuestID,
			OwnerNodeID: sourceA.id, OwnerEpoch: 1, ExpiresAt: requestedAt.Add(time.Hour), Version: 1,
		}).Error; err != nil {
			t.Fatalf("seed lease on %s: %v", node.id, err)
		}
		jobs := []clusterModels.BackupJob{
			{
				ID: 201, Name: "valid", TargetID: 1, RunnerNodeID: sourceA.id,
				Mode: clusterModels.BackupJobModeVM, SourceDataset: "fast/sylve/virtual-machines/808",
				DestSuffix: "virtual-machines/808/j-3a/active", Recursive: true,
				CronExpr: "0 0 * * *", Enabled: true,
			},
			{
				ID: 202, Name: "legacy-invalid", TargetID: 1, RunnerNodeID: sourceA.id,
				Mode: clusterModels.BackupJobModeVM, SourceDataset: "fast/sylve/virtual-machines/808",
				DestSuffix: "virtual-machines/808/j-3b/active", Recursive: false,
				CronExpr: "0 1 * * *", Enabled: true,
			},
		}
		if err := node.service.DB.Create(&jobs).Error; err != nil {
			t.Fatalf("seed jobs on %s: %v", node.id, err)
		}
	}
	preflightSim := newClusterPeerSimulator()
	registerBackupJobValidationPeer(t, preflightSim, sourceA.service)
	for _, node := range nodes {
		node.service.AuthService = clusterAuthStub{}
		node.service.backupJobValidationAPIForNode = func(nodeID string, _ raft.ServerAddress) (string, error) {
			if nodeID != sourceA.id {
				return "", fmt.Errorf("unexpected preflight runner %s", nodeID)
			}
			return preflightSim.Addr(), nil
		}
	}
	leaderC = applyBackupJobRebindTestLeaderCommand(t, nodes, func(leader *clusterRaftTestNode) error {
		return leader.service.PrepareBackupJobRunnerRebindForFailover(
			context.Background(), clusterModels.BackupJobModeVM, 808, targetB.id, runID, false,
		)
	})
	preflightSim.Close()

	cutoverAt := time.Now().UTC()
	newSource := targetB.id
	cutover := clusterModels.ReplicationOwnershipTransitionPayload{
		PolicyID: 808, ExpectedActiveNodeID: sourceA.id, ExpectedOwnerEpoch: 1,
		ExpectedTransitionRunID: runID, BackupJobRunnerRebindToken: runID,
		ActiveNodeID: targetB.id, SourceNodeID: &newSource, OwnerEpoch: 2,
		ReplaceTargets: true,
		Targets:        []clusterModels.ReplicationPolicyTarget{{NodeID: sourceA.id, Weight: 100}},
		Lease: clusterModels.ReplicationLease{
			PolicyID: 808, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 808,
			OwnerNodeID: targetB.id, OwnerEpoch: 2, ExpiresAt: cutoverAt.Add(time.Hour),
			Version: 2, LastReason: "manual_failover", LastActor: leaderC.id,
		},
		Transition: clusterModels.ReplicationPolicyTransition{
			State: clusterModels.ReplicationTransitionStatePromoting, RunID: runID,
			Reason: "manual_failover", SourceNodeID: sourceA.id, TargetNodeID: targetB.id,
			OwnerEpoch: 2, RequestedAt: &requestedAt, TriggerValidationRun: true,
			OriginalRunning: &originalRunning, OriginalSourceNodeID: sourceA.id,
		},
		ProtectionState: clusterModels.ReplicationProtectionStateDegraded,
	}
	leaderC = applyBackupJobRebindTestLeaderCommand(t, nodes, func(leader *clusterRaftTestNode) error {
		return leader.service.CommitReplicationOwnershipTransition(cutover, false)
	})
	var pendingJob clusterModels.BackupJob
	if err := leaderC.service.DB.First(&pendingJob, 201).Error; err != nil {
		t.Fatal(err)
	}
	if pendingJob.RunnerNodeID != sourceA.id || pendingJob.LastStatus != clusterModels.BackupJobRunnerRebindStatusPending {
		t.Fatalf("cutover did not atomically fence old runner: %+v", pendingJob)
	}
	if err := clusterModels.AcquireBackupJobOperationTxn(leaderC.service.DB, &clusterModels.BackupJobOperationAcquire{
		JobID: 201, Token: "backup:old-owner:blocked", Operation: clusterModels.BackupJobOperationBackup,
		HolderNodeID: sourceA.id, AcquiredAt: time.Now().UTC(),
	}); err == nil || !strings.Contains(err.Error(), "backup_job_runner_rebind_pending") {
		t.Fatalf("old owner acquired pending job: %v", err)
	}

	completedAt := time.Now().UTC()
	completedTransition := cutover.Transition
	completedTransition.State = clusterModels.ReplicationTransitionStateCompleted
	completedTransition.PromotedAt = &completedAt
	completedTransition.CompletedAt = &completedAt
	leaderC = applyBackupJobRebindTestLeaderCommand(t, nodes, func(leader *clusterRaftTestNode) error {
		return leader.service.UpdateReplicationPolicyTransition(808, completedTransition)
	})
	leaderC = waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if err := leaderC.service.BeginReplicationPolicyTransition(clusterModels.ReplicationPolicyTransitionBegin{
		PolicyID: 808, ExpectedOwnerEpoch: 2,
		Transition: clusterModels.ReplicationPolicyTransition{
			State: clusterModels.ReplicationTransitionStateDemoting, RunID: "failover-808-next",
			Reason: "manual_failover", SourceNodeID: targetB.id, TargetNodeID: sourceA.id, OwnerEpoch: 2,
		},
	}, false); err == nil || !strings.Contains(err.Error(), "backup_job_runner_rebind_pending") {
		t.Fatalf("new ownership transition was not fenced by pending rebind: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "leadership transfer", func() bool {
		if sourceA.raft.State() == raft.Leader {
			return true
		}
		current := findClusterRaftLeader(nodes)
		if current != nil {
			_ = current.raft.LeadershipTransferToServer(raft.ServerID(sourceA.id), sourceA.addr).Error()
		}
		return false
	})
	newLeader := sourceA
	newLeader.service.backupJobValidationAPIForNode = func(nodeID string, _ raft.ServerAddress) (string, error) {
		if nodeID != targetB.id {
			return "", fmt.Errorf("unexpected target runner %s", nodeID)
		}
		return "127.0.0.1:1", nil
	}
	if err := newLeader.service.ReconcileBackupJobRunnerRebind(context.Background(), runID); err == nil ||
		!strings.Contains(err.Error(), "backup_job_runner_rebind_pending") {
		t.Fatalf("post-completion transient failure did not remain pending: %v", err)
	}
	policies, err := newLeader.service.ListReplicationPolicies()
	if err != nil || len(policies) != 1 || !policies[0].HADegraded ||
		!slices.Contains(policies[0].HAReasons, "backup_job_runner_rebind_pending") {
		t.Fatalf("pending rebind not exposed in policy health: policies=%+v err=%v", policies, err)
	}

	targetSim := newClusterPeerSimulator()
	defer targetSim.Close()
	registerBackupJobValidationPeer(t, targetSim, targetB.service)
	newLeader.service.backupJobValidationAPIForNode = func(nodeID string, _ raft.ServerAddress) (string, error) {
		if nodeID != targetB.id {
			return "", fmt.Errorf("unexpected target runner %s", nodeID)
		}
		return targetSim.Addr(), nil
	}
	if err := newLeader.service.ReconcileBackupJobRunnerRebind(context.Background(), runID); err != nil {
		t.Fatalf("reconcile completed failover on new leader: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "failover rebind replication", func() bool {
		for _, node := range nodes {
			var operation clusterModels.BackupJobRunnerRebind
			if node.service.DB.First(&operation, "token = ?", runID).Error != nil ||
				operation.State != clusterModels.BackupJobRunnerRebindStateCompletedWithRepairs {
				return false
			}
			var valid, invalid clusterModels.BackupJob
			if node.service.DB.First(&valid, 201).Error != nil || valid.RunnerNodeID != targetB.id ||
				valid.LastStatus != "" || valid.LastError != "" {
				return false
			}
			if node.service.DB.First(&invalid, 202).Error != nil || invalid.RunnerNodeID != targetB.id ||
				invalid.Enabled || invalid.LastStatus != clusterModels.BackupJobRunnerRebindItemRepairRequired {
				return false
			}
		}
		return true
	})
	policies, err = newLeader.service.ListReplicationPolicies()
	if err != nil || len(policies) != 1 || !policies[0].HADegraded ||
		!slices.Contains(policies[0].HAReasons, "backup_job_runner_rebind_repair_required") {
		t.Fatalf("repair-required rebind not exposed in policy health: policies=%+v err=%v", policies, err)
	}
	if err := clusterModels.AcquireBackupJobOperationTxn(newLeader.service.DB, &clusterModels.BackupJobOperationAcquire{
		JobID: 201, Token: "backup:old-owner:after-rebind", Operation: clusterModels.BackupJobOperationBackup,
		HolderNodeID: sourceA.id, AcquiredAt: time.Now().UTC(),
	}); err == nil || !strings.Contains(err.Error(), "runner_mismatch") {
		t.Fatalf("old owner acquired rebound job: %v", err)
	}
}

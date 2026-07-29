// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/hashicorp/raft"
)

func registerBackupTargetValidationPeer(t *testing.T, sim *clusterPeerSimulator, service *Service) {
	t.Helper()
	sim.serveMux.HandleFunc(backupTargetValidationEndpoint, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request BackupTargetValidationRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := service.ValidateBackupTargetConnectivityLocal(r.Context(), request)
		if err != nil {
			_ = json.NewEncoder(w).Encode(internal.APIResponse[any]{
				Status: "error", Message: "backup_target_validation_failed", Error: err.Error(),
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(internal.APIResponse[clusterModels.BackupTargetNodeReadinessUpdate]{
			Status: "success", Data: result,
		})
	})
}

func TestBackupTargetValidationUsesSelectedVoterAndRecordsPerNodeOutcomes(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2,
		&clusterModels.BackupTarget{}, &clusterModels.BackupTargetNodeReadiness{},
		&clusterModels.BackupJob{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	runner := remoteClusterRaftTestNode(t, nodes, leader)
	for _, node := range nodes {
		node.service.NodeID = node.id
		if err := node.service.DB.Create(&clusterModels.BackupTarget{
			ID: 41, Name: "asymmetric", SSHHost: "root@backup", SSHPort: 22,
			BackupRoot: "tank/backups", Enabled: true,
		}).Error; err != nil {
			t.Fatalf("seed target on %s: %v", node.id, err)
		}
	}
	leader.service.AuthService = clusterAuthStub{}
	leader.service.SetBackupTargetValidator(func(context.Context, *clusterModels.BackupTarget) error {
		return errors.New("leader cannot reach target")
	})
	runner.service.SetBackupTargetValidator(func(context.Context, *clusterModels.BackupTarget) error { return nil })

	sim := newClusterPeerSimulator()
	defer sim.Close()
	registerBackupTargetValidationPeer(t, sim, runner.service)
	registerBackupJobValidationPeer(t, sim, runner.service)
	leader.service.backupTargetValidationAPIForNode = func(nodeID string, _ raft.ServerAddress) (string, error) {
		if nodeID != runner.id {
			t.Fatalf("target validation routed to %q, want %q", nodeID, runner.id)
		}
		return sim.Addr(), nil
	}
	leader.service.backupJobValidationAPIForNode = func(nodeID string, _ raft.ServerAddress) (string, error) {
		if nodeID != runner.id {
			t.Fatalf("job validation routed to %q, want %q", nodeID, runner.id)
		}
		return sim.Addr(), nil
	}

	remoteResult, err := leader.service.ValidateBackupTargetOnNode(
		context.Background(), 41, runner.id, leader.service.backupTargetValidator,
	)
	if err != nil || !remoteResult.ValidationSucceeded || remoteResult.NodeID != runner.id {
		t.Fatalf("runner validation result=%+v err=%v", remoteResult, err)
	}
	request := sim.FindRequest(backupTargetValidationEndpoint)
	if request == nil || request.Header.Get("X-Cluster-Token") != "Bearer test-cluster-token" {
		t.Fatalf("authenticated target validation request not observed: %+v", request)
	}

	localResult, err := leader.service.ValidateBackupTargetOnNode(
		context.Background(), 41, leader.id, leader.service.backupTargetValidator,
	)
	var rejected *BackupTargetValidationRejectedError
	if !errors.As(err, &rejected) || localResult.ValidationSucceeded ||
		!strings.Contains(localResult.LastError, "leader cannot reach") {
		t.Fatalf("leader validation result=%+v err=%v", localResult, err)
	}

	waitForClusterCondition(t, 5*time.Second, "per-node readiness replication", func() bool {
		for _, node := range nodes {
			var count int64
			if node.service.DB.Model(&clusterModels.BackupTargetNodeReadiness{}).
				Where("target_id = ?", 41).Count(&count).Error != nil || count != 2 {
				return false
			}
		}
		return true
	})
	statuses, err := leader.service.BackupTargetReadiness(41)
	if err != nil || len(statuses) != 2 {
		t.Fatalf("statuses=%+v err=%v", statuses, err)
	}
	statusByNode := make(map[string]clusterModels.BackupTargetNodeReadinessStatus, len(statuses))
	for _, status := range statuses {
		statusByNode[status.NodeID] = status
	}
	if !statusByNode[runner.id].Ready || statusByNode[leader.id].Ready {
		t.Fatalf("unexpected effective readiness: %+v", statuses)
	}

	enabled := true
	if err := leader.service.ProposeBackupJobCreateContext(context.Background(), clusterServiceInterfaces.BackupJobReq{
		Name: "runner-reachable", TargetID: 41, RunnerNodeID: runner.id,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/source",
		CronExpr: "0 0 * * *", Enabled: &enabled,
	}, false); err != nil {
		t.Fatalf("runner-reachable job create: %v", err)
	}
	if err := leader.service.ProposeBackupJobCreateContext(context.Background(), clusterServiceInterfaces.BackupJobReq{
		Name: "leader-unreachable", TargetID: 41, RunnerNodeID: leader.id,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/source-two",
		CronExpr: "0 1 * * *", Enabled: &enabled,
	}, false); err == nil || !strings.Contains(err.Error(), "backup_target_validation_rejected") {
		t.Fatalf("leader-unreachable job error = %v", err)
	}
	var jobCount int64
	if err := leader.service.DB.Model(&clusterModels.BackupJob{}).Count(&jobCount).Error; err != nil || jobCount != 1 {
		t.Fatalf("job count=%d err=%v", jobCount, err)
	}

	leader.service.SetBackupTargetValidator(func(context.Context, *clusterModels.BackupTarget) error { return nil })
	runner.service.SetBackupTargetValidator(func(context.Context, *clusterModels.BackupTarget) error {
		return errors.New("runner cannot reach target")
	})
	remoteResult, err = leader.service.ValidateBackupTargetOnNode(
		context.Background(), 41, runner.id, leader.service.backupTargetValidator,
	)
	if !errors.As(err, &rejected) || remoteResult.ValidationSucceeded {
		t.Fatalf("runner-only failure result=%+v err=%v", remoteResult, err)
	}
	localResult, err = leader.service.ValidateBackupTargetOnNode(
		context.Background(), 41, leader.id, leader.service.backupTargetValidator,
	)
	if err != nil || !localResult.ValidationSucceeded {
		t.Fatalf("leader-only success result=%+v err=%v", localResult, err)
	}

	if err := leader.raft.RemoveServer(raft.ServerID(runner.id), 0, 5*time.Second).Error(); err != nil {
		t.Fatalf("remove runner voter: %v", err)
	}
	waitForClusterCondition(t, 5*time.Second, "runner membership removal", func() bool {
		configuration := leader.raft.GetConfiguration()
		return configuration.Error() == nil && len(configuration.Configuration().Servers) == 1
	})
	statuses, err = leader.service.BackupTargetReadiness(41)
	if err != nil {
		t.Fatalf("readiness after runner removal: %v", err)
	}
	for _, status := range statuses {
		if status.NodeID == runner.id && (status.CurrentVoter || status.Ready) {
			t.Fatalf("removed runner remained ready: %+v", status)
		}
	}
}

func TestBackupTargetValidationReceiptRejectsStaleOrExtendedReadiness(t *testing.T) {
	now := time.Now().UTC()
	request := BackupTargetValidationRequest{
		ExpectedNodeID: "node-a", TargetID: 5, TargetFingerprint: strings.Repeat("a", 64),
	}
	readyUntil := now.Add(BackupTargetReadinessTTL)
	update := &clusterModels.BackupTargetNodeReadinessUpdate{
		TargetID: 5, NodeID: "node-a", TargetFingerprint: request.TargetFingerprint,
		ValidationSucceeded: true, LastVerifiedAt: now, ReadyUntil: &readyUntil,
	}
	if err := validateBackupTargetReadinessReceipt(request, update); err != nil {
		t.Fatalf("valid receipt: %v", err)
	}
	stale := *update
	stale.LastVerifiedAt = now.Add(-backupTargetValidationReceiptClockSkew - time.Second)
	staleUntil := stale.LastVerifiedAt.Add(BackupTargetReadinessTTL)
	stale.ReadyUntil = &staleUntil
	if err := validateBackupTargetReadinessReceipt(request, &stale); err == nil ||
		!strings.Contains(err.Error(), "timestamp_stale") {
		t.Fatalf("stale receipt error = %v", err)
	}
	extended := *update
	extendedUntil := now.Add(24 * time.Hour)
	extended.ReadyUntil = &extendedUntil
	if err := validateBackupTargetReadinessReceipt(request, &extended); err == nil ||
		!strings.Contains(err.Error(), "expiry_invalid") {
		t.Fatalf("extended receipt error = %v", err)
	}
}

func TestBackupTargetReadinessSurvivesLeadershipChange(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3,
		&clusterModels.BackupTarget{}, &clusterModels.BackupTargetNodeReadiness{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	var nextLeader *clusterRaftTestNode
	for _, node := range nodes {
		node.service.NodeID = node.id
		if node != leader && nextLeader == nil {
			nextLeader = node
		}
		if err := node.service.DB.Create(&clusterModels.BackupTarget{
			ID: 61, Name: "leadership", SSHHost: "root@backup", SSHPort: 22,
			BackupRoot: "tank/backups", Enabled: true,
		}).Error; err != nil {
			t.Fatalf("seed target on %s: %v", node.id, err)
		}
	}
	var target clusterModels.BackupTarget
	if err := leader.service.DB.First(&target, 61).Error; err != nil {
		t.Fatalf("load target: %v", err)
	}
	verifiedAt := time.Now().UTC()
	readyUntil := verifiedAt.Add(BackupTargetReadinessTTL)
	if err := leader.service.UpdateBackupTargetNodeReadiness(clusterModels.BackupTargetNodeReadinessUpdate{
		TargetID: target.ID, NodeID: nextLeader.id,
		TargetFingerprint:   clusterModels.BackupTargetConnectivityFingerprint(&target),
		ValidationSucceeded: true, LastVerifiedAt: verifiedAt, ReadyUntil: &readyUntil,
	}, false); err != nil {
		t.Fatalf("publish readiness: %v", err)
	}
	if err := leader.raft.LeadershipTransferToServer(raft.ServerID(nextLeader.id), nextLeader.addr).Error(); err != nil {
		t.Fatalf("leadership transfer: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "readiness leader transfer", func() bool {
		return nextLeader.raft.State() == raft.Leader
	})
	statuses, err := nextLeader.service.BackupTargetReadiness(target.ID)
	if err != nil {
		t.Fatalf("readiness on new leader: %v", err)
	}
	for _, status := range statuses {
		if status.NodeID == nextLeader.id {
			if !status.Ready || status.Revision != 1 {
				t.Fatalf("new leader readiness: %+v", status)
			}
			return
		}
	}
	t.Fatalf("new leader readiness missing: %+v", statuses)
}

func TestBackupTargetReadinessEffectiveStatusHandlesExpiryAndRemovedVoter(t *testing.T) {
	now := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	target := clusterModels.BackupTarget{
		ID: 51, SSHHost: "root@backup", SSHPort: 22, BackupRoot: "tank/backups",
	}
	fingerprint := clusterModels.BackupTargetConnectivityFingerprint(&target)
	expiredAt := now.Add(-time.Minute)
	future := now.Add(time.Minute)
	rows := []clusterModels.BackupTargetNodeReadiness{
		{TargetID: target.ID, NodeID: "expired-voter", TargetFingerprint: fingerprint,
			ValidationSucceeded: true, LastVerifiedAt: now.Add(-time.Hour), ReadyUntil: &expiredAt, Revision: 1},
		{TargetID: target.ID, NodeID: "removed-voter", TargetFingerprint: fingerprint,
			ValidationSucceeded: true, LastVerifiedAt: now, ReadyUntil: &future, Revision: 1},
	}
	statuses := backupTargetReadinessStatuses(target, rows, []string{"expired-voter", "never-validated"}, now)
	byNode := make(map[string]clusterModels.BackupTargetNodeReadinessStatus, len(statuses))
	for _, status := range statuses {
		byNode[status.NodeID] = status
	}
	if !byNode["expired-voter"].Expired || byNode["expired-voter"].Ready {
		t.Fatalf("expired voter status: %+v", byNode["expired-voter"])
	}
	if byNode["removed-voter"].CurrentVoter || byNode["removed-voter"].Ready {
		t.Fatalf("removed voter status: %+v", byNode["removed-voter"])
	}
	if byNode["never-validated"].LastVerifiedAt != nil || byNode["never-validated"].Ready {
		t.Fatalf("never-validated status: %+v", byNode["never-validated"])
	}
}

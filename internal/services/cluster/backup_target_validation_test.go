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

func TestBackupTargetValidationRecordsPerNodeOutcomes(t *testing.T) {
	target := clusterModels.BackupTarget{
		ID: 41, Name: "asymmetric", SSHHost: "root@backup", SSHPort: 22,
		BackupRoot: "tank/backups", Enabled: true,
	}
	leader := &Service{
		DB:          newClusterServiceTestDB(t, &clusterModels.BackupTarget{}),
		NodeID:      "node-leader",
		AuthService: clusterAuthStub{},
	}
	runner := &Service{
		DB:     newClusterServiceTestDB(t, &clusterModels.BackupTarget{}),
		NodeID: "node-runner",
	}
	for _, service := range []*Service{leader, runner} {
		if err := service.DB.Create(&target).Error; err != nil {
			t.Fatalf("seed target on %s: %v", service.NodeID, err)
		}
	}
	runner.SetBackupTargetValidator(func(context.Context, *clusterModels.BackupTarget) error { return nil })

	sim := newClusterPeerSimulator()
	defer sim.Close()
	registerBackupTargetValidationPeer(t, sim, runner)
	request := BackupTargetValidationRequest{
		ExpectedNodeID: "node-runner", TargetID: target.ID,
		TargetFingerprint: clusterModels.BackupTargetConnectivityFingerprint(&target),
	}
	remote, err := leader.fetchBackupTargetValidation(t.Context(), "node-runner", sim.Addr(), request)
	if err != nil {
		t.Fatalf("validate remote target: %v", err)
	}
	if err := validateBackupTargetReadinessReceipt(request, &remote); err != nil {
		t.Fatalf("validate remote receipt: %v", err)
	}
	if err := leader.UpdateBackupTargetNodeReadiness(remote, true); err != nil {
		t.Fatalf("record remote readiness: %v", err)
	}
	captured := sim.FindRequest(backupTargetValidationEndpoint)
	if captured == nil || captured.Header.Get("X-Cluster-Token") != "Bearer test-cluster-token" {
		t.Fatalf("authenticated request not observed: %+v", captured)
	}

	local, err := leader.ValidateBackupTargetOnNode(
		t.Context(), target.ID, "node-leader",
		func(context.Context, *clusterModels.BackupTarget) error {
			return errors.New("leader cannot reach target")
		},
	)
	var rejected *BackupTargetValidationRejectedError
	if !errors.As(err, &rejected) || local.ValidationSucceeded ||
		!strings.Contains(local.LastError, "leader cannot reach target") {
		t.Fatalf("local validation result=%+v err=%v", local, err)
	}

	var rows []clusterModels.BackupTargetNodeReadiness
	if err := leader.DB.Order("node_id").Find(&rows).Error; err != nil {
		t.Fatalf("load readiness rows: %v", err)
	}
	statuses := backupTargetReadinessStatuses(
		target, rows, []string{"node-leader", "node-runner"}, time.Now().UTC(),
	)
	if len(statuses) != 2 || statuses[0].Ready || !statuses[1].Ready {
		t.Fatalf("readiness statuses = %+v", statuses)
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

func TestIntegrationRaftBackupTargetReadinessSurvivesLeadershipChange(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3,
		&clusterModels.BackupTarget{}, &clusterModels.BackupTargetNodeReadiness{},
	)
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

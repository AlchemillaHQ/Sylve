// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestBackupTargetRestoreReservationSurvivesThreeVoterLeadershipChange(t *testing.T) {
	nodes := setupClusterRaftTestNodes(
		t,
		3,
		&clusterModels.BackupTarget{},
		&clusterModels.BackupJob{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)

	target := clusterModels.BackupTarget{
		ID: 91, Name: "target", SSHHost: "root@backup", SSHPort: 22,
		BackupRoot: "tank/backups", Enabled: true,
	}
	for _, node := range nodes {
		if err := node.service.DB.Create(&target).Error; err != nil {
			t.Fatalf("seed target on %s: %v", node.id, err)
		}
	}

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	now := time.Date(2026, time.April, 5, 6, 7, 8, 0, time.UTC)
	acquire := clusterModels.BackupTargetRestoreOperationAcquire{
		Token: "target-restore:node-2:leadership", TargetID: target.ID, HolderNodeID: "node-2",
		DestinationDataset: "zroot/restored", RequestPayload: `{"targetId":91,"snapshot":"@bk_j1_c1_test"}`,
		AcquiredAt: now,
	}
	if err := leader.service.AcquireBackupTargetRestoreOperation(acquire, false); err != nil {
		t.Fatalf("acquire on leader %s: %v", leader.id, err)
	}
	waitForClusterCondition(t, 8*time.Second, "queued target restore replication", func() bool {
		for _, node := range nodes {
			var operation clusterModels.BackupTargetRestoreOperation
			if err := node.service.DB.First(&operation, "token = ?", acquire.Token).Error; err != nil ||
				operation.State != clusterModels.BackupTargetRestoreOperationQueued {
				return false
			}
		}
		return true
	})

	conflict := acquire
	conflict.Token = "target-restore:node-2:overlap"
	conflict.DestinationDataset = "zroot/restored/child"
	conflict.AcquiredAt = now.Add(time.Second)
	if err := leader.service.AcquireBackupTargetRestoreOperation(conflict, false); err == nil ||
		!strings.Contains(err.Error(), "restore_destination_reserved") {
		t.Fatalf("overlapping acquire error = %v", err)
	}

	oldLeaderID := leader.id
	if err := leader.raft.Shutdown().Error(); err != nil {
		t.Fatalf("shutdown old leader %s: %v", oldLeaderID, err)
	}
	remaining := make([]*clusterRaftTestNode, 0, 2)
	for _, node := range nodes {
		if node.id != oldLeaderID {
			remaining = append(remaining, node)
		}
	}
	newLeader := waitForClusterRaftLeader(t, remaining, 8*time.Second)

	transition := clusterModels.BackupTargetRestoreOperationTransition{
		Token: acquire.Token, TargetID: acquire.TargetID, HolderNodeID: acquire.HolderNodeID,
		DestinationDataset: acquire.DestinationDataset, RequestPayload: acquire.RequestPayload,
		OccurredAt: now.Add(2 * time.Second),
	}
	if err := newLeader.service.TransitionBackupTargetRestoreOperation("start", transition, false); err != nil {
		t.Fatalf("start after leadership change on %s: %v", newLeader.id, err)
	}
	if err := newLeader.service.TransitionBackupTargetRestoreOperation("start", transition, false); err == nil ||
		!strings.Contains(err.Error(), "already_started") {
		t.Fatalf("duplicate start error = %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "running target restore replication", func() bool {
		for _, node := range remaining {
			var operation clusterModels.BackupTargetRestoreOperation
			if err := node.service.DB.First(&operation, "token = ?", acquire.Token).Error; err != nil ||
				operation.State != clusterModels.BackupTargetRestoreOperationRunning || operation.Revision != 2 {
				return false
			}
		}
		return true
	})

	transition.OccurredAt = now.Add(3 * time.Second)
	if err := newLeader.service.TransitionBackupTargetRestoreOperation("finish", transition, false); err != nil {
		t.Fatalf("finish: %v", err)
	}
	transition.OccurredAt = now.Add(4 * time.Second)
	if err := newLeader.service.TransitionBackupTargetRestoreOperation("release", transition, false); err != nil {
		t.Fatalf("release: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "target restore completion replication", func() bool {
		for _, node := range remaining {
			var operation clusterModels.BackupTargetRestoreOperation
			if err := node.service.DB.First(&operation, "token = ?", acquire.Token).Error; err != nil ||
				operation.State != clusterModels.BackupTargetRestoreOperationCompleted || operation.Revision != 4 {
				return false
			}
		}
		return true
	})
}

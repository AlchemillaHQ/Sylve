// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestReplicationTransitionEventsAndRetentionConvergeWithoutMergingLocalHistory(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2,
		&clusterModels.Cluster{},
		&clusterModels.ReplicationEvent{},
		&clusterModels.ReplicationGuestOperationReceipt{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)

	for i, node := range nodes {
		if err := node.service.DB.Create(&clusterModels.Cluster{
			Enabled: true,
		}).Error; err != nil {
			t.Fatalf("seed cluster state on node %d: %v", i+1, err)
		}
		if err := node.service.DB.Create(&clusterModels.ReplicationEvent{
			ID: 1, EventType: "replication", Status: "success",
			Message: "local-node-" + node.id, StartedAt: time.Now().UTC(),
		}).Error; err != nil {
			t.Fatalf("seed local event on %s: %v", node.id, err)
		}
	}

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	policyID := uint(9)
	eventID, err := leader.service.CreateOrUpdateReplicationEvent(clusterModels.ReplicationEvent{
		PolicyID: &policyID, TransitionRunID: "transition-converged",
		EventType: "failover", Status: "active", StartedAt: time.Now().UTC(),
	}, false)
	if err != nil {
		t.Fatalf("create transition event: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "transition event convergence", func() bool {
		for _, node := range nodes {
			var transition clusterModels.ReplicationTransitionEvent
			if err := node.service.DB.First(&transition, eventID).Error; err != nil ||
				transition.TransitionRunID != "transition-converged" {
				return false
			}
			var local clusterModels.ReplicationEvent
			if err := node.service.DB.First(&local, 1).Error; err != nil ||
				local.Message != "local-node-"+node.id {
				return false
			}
		}
		return true
	})

	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	expiredAt := now.Add(-100 * 24 * time.Hour)
	for _, node := range nodes {
		if err := node.service.DB.Create(&clusterModels.ScheduledRunReceipt{
			Token: "expired-receipt", Kind: clusterModels.ScheduledRunKindBackup,
			ObjectID: 1, HolderNodeID: "node-1", Status: "success", CompletedAt: expiredAt,
		}).Error; err != nil {
			t.Fatalf("seed expired receipt on %s: %v", node.id, err)
		}
		if err := node.service.DB.Create(&clusterModels.ReplicationTransitionEvent{
			ID: 2, TransitionRunID: "expired-transition", EventType: "failover",
			Status: "failed", StartedAt: expiredAt, CompletedAt: &expiredAt,
		}).Error; err != nil {
			t.Fatalf("seed expired transition on %s: %v", node.id, err)
		}
		if err := node.service.DB.Create(&clusterModels.ReplicationGuestOperationReceipt{
			Token: "expired-guest-receipt", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 1, Operation: clusterModels.ReplicationGuestOperationMigration,
			OwnerNodeID: "node-1", TargetNodeID: "node-2", TaskID: 1,
			AcquiredAt: expiredAt.Add(-time.Minute), CompletedAt: expiredAt,
		}).Error; err != nil {
			t.Fatalf("seed expired guest receipt on %s: %v", node.id, err)
		}
	}

	if err := leader.service.EnforceReplicatedRetention(now); err != nil {
		t.Fatalf("enforce replicated retention: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "replicated retention convergence", func() bool {
		for _, node := range nodes {
			var receiptCount int64
			node.service.DB.Model(&clusterModels.ScheduledRunReceipt{}).Count(&receiptCount)
			if receiptCount != 0 {
				return false
			}
			var guestReceiptCount int64
			node.service.DB.Model(&clusterModels.ReplicationGuestOperationReceipt{}).Count(&guestReceiptCount)
			if guestReceiptCount != 0 {
				return false
			}
			var expiredTransitionCount int64
			node.service.DB.Model(&clusterModels.ReplicationTransitionEvent{}).
				Where("id = ?", 2).
				Count(&expiredTransitionCount)
			if expiredTransitionCount != 0 {
				return false
			}
			var local clusterModels.ReplicationEvent
			if err := node.service.DB.First(&local, 1).Error; err != nil ||
				local.Message != "local-node-"+node.id {
				return false
			}
		}
		return true
	})
}

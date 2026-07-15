// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestRaftReplicationGuestOperationSurvivesLeaderFailover(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationGuestOperation{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)

	initialLeader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	policy := clusterModels.ReplicationPolicy{
		ID: 1, Name: "migration-guarded", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 901,
		SourceNodeID: "node-1", ActiveNodeID: "node-1", OwnerEpoch: 1,
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "0 * * * *", Enabled: false,
		ProtectionState: clusterModels.ReplicationProtectionStateUnprotected,
		TransitionState: clusterModels.ReplicationTransitionStateNone,
	}
	createRaw, _ := json.Marshal(clusterModels.ReplicationPolicyPayload{Policy: policy})
	if err := initialLeader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_policy", Action: "create", Data: createRaw,
	}); err != nil {
		t.Fatalf("create disabled policy: %v", err)
	}
	if err := initialLeader.service.AcquireReplicationGuestOperation(clusterModels.ReplicationGuestOperationAcquire{
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 901,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		Token:     "migration:node-1:901", OwnerNodeID: "node-1", TargetNodeID: "node-2", TaskID: 901,
	}, false); err != nil {
		t.Fatalf("acquire guest operation: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "guest operation replication", func() bool {
		for _, node := range nodes {
			var operation clusterModels.ReplicationGuestOperation
			if err := node.service.DB.First(&operation, "guest_type = ? AND guest_id = ?", "vm", 901).Error; err != nil ||
				operation.Token != "migration:node-1:901" {
				return false
			}
		}
		return true
	})

	staleEnable := policy
	staleEnable.Enabled = true
	staleEnable.ProtectionState = clusterModels.ReplicationProtectionStateInitializing
	updateRaw, _ := json.Marshal(clusterModels.ReplicationPolicyPayload{
		Policy: staleEnable, ExpectedOwnerEpoch: 1,
	})
	if err := initialLeader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_policy", Action: "update", Data: updateRaw,
	}); err == nil || !strings.Contains(err.Error(), "guest_operation_in_progress") {
		t.Fatalf("migration guard did not reject stale enable: %v", err)
	}

	survivors := make([]*clusterRaftTestNode, 0, 2)
	for _, node := range nodes {
		if node.id != initialLeader.id {
			survivors = append(survivors, node)
			node.transport.Disconnect(initialLeader.addr)
		}
	}
	initialLeader.transport.DisconnectAll()
	_ = initialLeader.raft.Shutdown().Error()
	newLeader := waitForClusterRaftLeader(t, survivors, 12*time.Second)

	if err := newLeader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_policy", Action: "update", Data: updateRaw,
	}); err == nil || !strings.Contains(err.Error(), "guest_operation_in_progress") {
		t.Fatalf("guard was lost across leader failover: %v", err)
	}
	if err := newLeader.service.AbortReplicationGuestOperation(clusterModels.ReplicationGuestOperationTransition{
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 901,
		Operation: clusterModels.ReplicationGuestOperationMigration, Token: "migration:node-1:901",
	}, false); err != nil {
		t.Fatalf("abort guard on new leader: %v", err)
	}
	if err := newLeader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_policy", Action: "update", Data: updateRaw,
	}); err != nil {
		t.Fatalf("enable after exact abort: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "post-abort enable replication", func() bool {
		for _, node := range survivors {
			var persisted clusterModels.ReplicationPolicy
			if err := node.service.DB.First(&persisted, policy.ID).Error; err != nil || !persisted.Enabled {
				return false
			}
			var count int64
			node.service.DB.Model(&clusterModels.ReplicationGuestOperation{}).Count(&count)
			if count != 0 {
				return false
			}
		}
		return true
	})
}

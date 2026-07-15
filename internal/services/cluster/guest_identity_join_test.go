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
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/hashicorp/raft"
)

const guestIdentityJoinTestKey = "guest-identity-join-test-key"

func guestIdentityJoinTestModels() []any {
	return []any{
		&clusterModels.Cluster{},
		&clusterModels.ClusterNode{},
		&clusterModels.ClusterOption{},
		&clusterModels.ClusterNote{},
		&clusterModels.BackupTarget{},
		&clusterModels.BackupJob{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationGuestOperation{},
		&clusterModels.ReplicationGuestOperationReceipt{},
		&clusterModels.ReplicationEvent{},
		&clusterModels.ClusterSSHIdentity{},
		&clusterModels.EncryptionKey{},
		&vmModels.VM{},
		&jailModels.Jail{},
	}
}

func seedGuestIdentityJoinTestCluster(t *testing.T, node *clusterRaftTestNode) {
	t.Helper()
	node.service.NodeID = node.id
	if err := node.service.DB.Create(&clusterModels.Cluster{
		Enabled:  true,
		Key:      guestIdentityJoinTestKey,
		RaftIP:   "127.0.0.1",
		RaftPort: ClusterRaftPort,
	}).Error; err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
}

func raftConfigurationForGuestIdentityJoinTest(t *testing.T, node *clusterRaftTestNode) raft.Configuration {
	t.Helper()
	future := node.raft.GetConfiguration()
	if err := future.Error(); err != nil {
		t.Fatalf("get Raft configuration: %v", err)
	}
	return future.Configuration()
}

func TestCanonicalSubmittedGuestIdentityInventoryRejectsDigestAndNodeMismatch(t *testing.T) {
	t.Run("digest mismatch", func(t *testing.T) {
		submitted := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
			NodeID: "joiner", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 100, RecordID: 1, Name: "vm-100",
		}})
		submitted.Digest = strings.Repeat("0", 64)

		_, err := canonicalSubmittedGuestIdentityInventory("joiner", submitted)
		if err == nil || !strings.Contains(err.Error(), "joining_inventory_digest_mismatch") {
			t.Fatalf("error = %v, want digest mismatch", err)
		}
	})

	t.Run("node mismatch", func(t *testing.T) {
		submitted := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
			NodeID: "different-node", GuestType: clusterModels.ReplicationGuestTypeJail,
			GuestID: 101, RecordID: 2, Name: "jail-101",
		}})

		_, err := canonicalSubmittedGuestIdentityInventory("joiner", submitted)
		if err == nil || !strings.Contains(err.Error(), "joining_inventory_node_mismatch") {
			t.Fatalf("error = %v, want node mismatch", err)
		}
	})
}

func TestPreflightJoinInventorySharedIDIsReadOnly(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, guestIdentityJoinTestModels()...)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)

	if err := leader.service.DB.Create(&vmModels.VM{RID: 200, Name: "leader-vm"}).Error; err != nil {
		t.Fatalf("seed leader VM: %v", err)
	}
	joiner := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
		NodeID: "joining-node", GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID: 200, RecordID: 1, Name: "joiner-jail",
	}})
	before := raftConfigurationForGuestIdentityJoinTest(t, leader)

	_, err := leader.service.PreflightJoinInventory(
		context.Background(), "joining-node", "127.0.0.2", guestIdentityJoinTestKey, joiner,
	)
	var conflict *GuestIdentityInventoryConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want inventory conflict", err)
	}
	if len(conflict.Report.Conflicts) != 1 ||
		conflict.Report.Conflicts[0].Reason != GuestIdentityInventoryConflictSharedGuestID {
		t.Fatalf("unexpected conflict report: %+v", conflict.Report)
	}

	after := raftConfigurationForGuestIdentityJoinTest(t, leader)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("preflight mutated membership: before=%+v after=%+v", before, after)
	}
}

func TestAcceptJoinInventoryRejectsConflictIntroducedAfterPreflight(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, guestIdentityJoinTestModels()...)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)

	joiner := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
		NodeID: "joining-node", GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID: 301, RecordID: 1, Name: "joiner-jail",
	}})
	if _, err := leader.service.PreflightJoinInventory(
		context.Background(), "joining-node", "127.0.0.2", guestIdentityJoinTestKey, joiner,
	); err != nil {
		t.Fatalf("initial preflight: %v", err)
	}
	if err := leader.service.DB.Create(&vmModels.VM{RID: 301, Name: "late-leader-vm"}).Error; err != nil {
		t.Fatalf("seed late conflicting VM: %v", err)
	}

	before := raftConfigurationForGuestIdentityJoinTest(t, leader)
	err := leader.service.AcceptJoinInventory(
		context.Background(), "joining-node", "127.0.0.2", guestIdentityJoinTestKey, joiner,
	)
	var conflict *GuestIdentityInventoryConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want final-recheck inventory conflict", err)
	}
	after := raftConfigurationForGuestIdentityJoinTest(t, leader)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("failed final check mutated membership: before=%+v after=%+v", before, after)
	}
}

func TestValidateJoinMembershipNeverReplacesConflictingServer(t *testing.T) {
	configuration := raft.Configuration{Servers: []raft.Server{
		{ID: "leader", Address: "127.0.0.1:8180", Suffrage: raft.Voter},
		{ID: "existing", Address: "127.0.0.2:8180", Suffrage: raft.Voter},
	}}
	original := append([]raft.Server(nil), configuration.Servers...)

	tests := []struct {
		name     string
		nodeID   string
		address  raft.ServerAddress
		wantText string
	}{
		{"same ID different address", "existing", "127.0.0.9:8180", "joining_node_id_already_in_use"},
		{"different ID same address", "new-node", "127.0.0.2:8180", "joining_node_address_already_in_use"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alreadyVoter, err := validateJoinMembership(configuration, "leader", tt.nodeID, tt.address)
			if alreadyVoter || err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("alreadyVoter=%v error=%v, want %s", alreadyVoter, err, tt.wantText)
			}
			if !reflect.DeepEqual(configuration.Servers, original) {
				t.Fatalf("membership validation mutated configuration: %+v", configuration.Servers)
			}
		})
	}
}

func TestAcceptJoinInventoryAndExactExistingVoterRetry(t *testing.T) {
	models := guestIdentityJoinTestModels()
	nodes := setupClusterRaftTestNodes(t, 1, models...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)

	joinerIP := "127.0.0.2"
	joinerID := RaftServerAddress(joinerIP)
	joiner := newClusterRaftTestNode(t, joinerID, models...)
	nodes = append(nodes, joiner)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader.transport.Connect(joiner.addr, joiner.transport)
	joiner.transport.Connect(leader.addr, leader.transport)

	if err := leader.service.DB.Create(&vmModels.VM{RID: 500, Name: "leader-vm"}).Error; err != nil {
		t.Fatalf("seed leader VM: %v", err)
	}
	joinerReport := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
		NodeID: joinerID, GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID: 501, RecordID: 7, Name: "joiner-jail",
	}})
	sim := newClusterPeerSimulator()
	defer sim.Close()
	registerGuestIdentityInventoryPeer(t, sim, joinerID, joinerReport.Entries)
	leader.service.AuthService = &guestIdentityInventoryAuthStub{}
	leader.service.guestIdentityInventoryAPIForNode = func(
		nodeID string,
		_ raft.ServerAddress,
	) (string, error) {
		if nodeID != joinerID {
			return "", errors.New("unexpected inventory node")
		}
		return sim.Addr(), nil
	}

	if err := leader.service.AcceptJoinInventory(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, joinerReport,
	); err != nil {
		t.Fatalf("accept clean join: %v", err)
	}
	waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)

	beforeRetry := raftConfigurationForGuestIdentityJoinTest(t, leader)
	if err := leader.service.AcceptJoinInventory(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, joinerReport,
	); err != nil {
		t.Fatalf("retry exact existing voter join: %v", err)
	}
	afterRetry := raftConfigurationForGuestIdentityJoinTest(t, leader)
	if !reflect.DeepEqual(afterRetry, beforeRetry) {
		t.Fatalf("exact voter retry changed membership: before=%+v after=%+v", beforeRetry, afterRetry)
	}
}

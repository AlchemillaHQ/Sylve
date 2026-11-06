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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/cmd"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

// The join key's pin goes unchecked over the tests' simulated network.
const guestIdentityJoinTestSecret = "guest-identity-join-test-secret"

var guestIdentityJoinTestKey = guestIdentityJoinTestSecret + "." + strings.Repeat("ab", 32)

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
		&clusterModels.GuestIdentityClaim{},
		&vmModels.VM{},
		&jailModels.Jail{},
		&taskModels.GuestLifecycleTask{},
	}
}

func seedGuestIdentityJoinTestCluster(t *testing.T, node *clusterRaftTestNode) {
	t.Helper()
	node.service.NodeID = node.id
	if err := node.service.DB.Create(&clusterModels.Cluster{
		Enabled:  true,
		RaftIP:   "127.0.0.1",
		RaftPort: ClusterRaftPort,
	}).Error; err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if node.raft.State() == raft.Leader {
		seedJoinKey(t, node)
	}
}

func seedGuestIdentityJoinTestClaim(
	t *testing.T,
	node *clusterRaftTestNode,
	ownerNodeID, guestKind string,
	guestID uint,
) {
	t.Helper()
	if err := node.service.apply("guest_identity", "claim", clusterModels.GuestIdentityClaimSet{
		OwnerNodeID: ownerNodeID,
		Entries:     []clusterModels.GuestIdentityEntry{{GuestKind: guestKind, GuestID: guestID}},
	}); err != nil {
		t.Fatalf("claim test guest identity: %v", err)
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

func TestResolveJoinMembershipNeverReplacesAConflictingServer(t *testing.T) {
	configuration := raft.Configuration{Servers: []raft.Server{
		{ID: "leader", Address: "127.0.0.1:8180", Suffrage: raft.Voter},
		{ID: "existing", Address: "127.0.0.2:8180", Suffrage: raft.Voter},
	}}

	tests := []struct {
		name     string
		nodeID   string
		address  raft.ServerAddress
		wantText string
	}{
		{"same ID different address", "existing", "127.0.0.9:8180", "joining_node_id_already_in_use"},
		{"different ID same address", "new-node", "127.0.0.2:8180", "joining_node_address_already_in_use"},
		{"the leader itself", "leader", "127.0.0.1:8180", "joining_node_id_conflicts_with_leader"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := resolveJoinMembership(configuration, "leader", tt.nodeID, tt.address)
			if server != nil || err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("server=%+v error=%v, want %s", server, err, tt.wantText)
			}
		})
	}
	if server, err := resolveJoinMembership(configuration, "leader", "existing", "127.0.0.2:8180"); err != nil || server == nil || server.ID != "existing" {
		t.Fatalf("exact member = %+v, %v", server, err)
	}
}

func TestAdmitJoinerRefusesAnotherVersion(t *testing.T) {
	service := withFSM(&Service{DB: newClusterServiceTestDB(t, &clusterModels.Cluster{}), mutationGate: newOpenTestMutationGate(t)})
	_, err := service.AdmitJoiner(context.Background(), JoinAdmissionRequest{
		NodeID: "joining-node", NodeIP: "127.0.0.2", NodeVersion: cmd.Version + "-other",
	}, guestIdentityJoinTestSecret, testJoinPin)
	var versionErr *ClusterVersionError
	if !errors.As(err, &versionErr) || versionErr.Code != "cluster_version_mismatch" {
		t.Fatalf("admission error = %v, want a version mismatch", err)
	}
}

func TestIntegrationRaftAdmitJoinerRefusesAHeldGuestID(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, guestIdentityJoinTestModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	seedGuestIdentityJoinTestClaim(t, leader, leader.id, clusterModels.ReplicationGuestTypeVM, 301)

	before := raftConfigurationForGuestIdentityJoinTest(t, leader)
	_, err := leader.service.AdmitJoiner(context.Background(), JoinAdmissionRequest{
		NodeID: "joining-node", NodeIP: "127.0.0.2", NodeVersion: cmd.Version, SSHPublicKey: "ssh-ed25519 AAAA",
		Inventory: BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{
			{NodeID: "joining-node", GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 300, RecordID: 1, Name: "free-jail"},
			{NodeID: "joining-node", GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 301, RecordID: 2, Name: "clashing-jail"},
		}),
	}, guestIdentityJoinTestSecret, testJoinPin)
	var conflict *GuestIdentityInventoryConflictError
	if !errors.As(err, &conflict) || len(conflict.Report.Conflicts) != 1 || conflict.Report.Conflicts[0].GuestID != 301 {
		t.Fatalf("admission error = %v, want a conflict on 301", err)
	}
	if after := raftConfigurationForGuestIdentityJoinTest(t, leader); !reflect.DeepEqual(after, before) {
		t.Fatalf("refused admission changed membership: before=%+v after=%+v", before, after)
	}
	claims, err := leader.service.ListGuestIdentityClaims(context.Background())
	if err != nil || len(claims) != 1 || claims[0].OwnerNodeID != leader.id {
		t.Fatalf("claims after refused admission = %+v, %v", claims, err)
	}
	if !clusterModels.JoinKeyMatches(leader.service.DB, guestIdentityJoinTestSecret) {
		t.Fatal("a refused admission spent the join key")
	}
}

func TestIntegrationRaftAdmitJoinerRefusesATakenHostname(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, guestIdentityJoinTestModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	if err := leader.service.DB.Create(&clusterModels.ClusterNode{NodeUUID: leader.id, Hostname: "taken"}).Error; err != nil {
		t.Fatal(err)
	}
	request := func(hostname string) JoinAdmissionRequest {
		return JoinAdmissionRequest{
			NodeID: "joining-node", NodeIP: "127.0.0.2", NodeVersion: cmd.Version, NodeHostname: hostname,
			SSHPublicKey: "ssh-ed25519 AAAA", Inventory: BuildGuestIdentityInventoryReport(nil),
		}
	}

	before := raftConfigurationForGuestIdentityJoinTest(t, leader)
	_, err := leader.service.AdmitJoiner(t.Context(), request("taken"), guestIdentityJoinTestSecret, testJoinPin)
	if err == nil || err.Error() != "joining_node_hostname_taken: "+leader.id {
		t.Fatalf("admission error = %v, want the hostname taken by %s", err, leader.id)
	}
	if after := raftConfigurationForGuestIdentityJoinTest(t, leader); !reflect.DeepEqual(after, before) {
		t.Fatalf("refused admission changed membership: before=%+v after=%+v", before, after)
	}
	if !clusterModels.JoinKeyMatches(leader.service.DB, guestIdentityJoinTestSecret) {
		t.Fatal("a refused admission spent the join key")
	}

	if _, err := leader.service.AdmitJoiner(t.Context(), request("fresh"), guestIdentityJoinTestSecret, testJoinPin); err != nil {
		t.Fatal(err)
	}
	if err := leader.service.DB.Create(&clusterModels.ClusterNode{NodeUUID: "joining-node", Hostname: "fresh"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := leader.service.AdmitJoiner(t.Context(), request("fresh"), "", testJoinPin); err != nil {
		t.Fatalf("the member asking again: %v", err)
	}
}

func TestIntegrationRaftJoinKeyAdmitsOneNode(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, guestIdentityJoinTestModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	request := func(nodeID, nodeIP string) JoinAdmissionRequest {
		return JoinAdmissionRequest{
			NodeID: nodeID, NodeIP: nodeIP, NodeVersion: cmd.Version, SSHPublicKey: "ssh-ed25519 AAAA",
			Inventory: BuildGuestIdentityInventoryReport(nil),
		}
	}
	if _, err := leader.service.AdmitJoiner(t.Context(), request("first", "127.0.0.2"), guestIdentityJoinTestSecret, testJoinPin); err != nil {
		t.Fatal(err)
	}
	if clusterModels.JoinKeyMatches(leader.service.DB, guestIdentityJoinTestSecret) {
		t.Fatal("admitting a node didn't spend the key")
	}
	if _, err := leader.service.AdmitJoiner(
		t.Context(), request("second", "127.0.0.3"), guestIdentityJoinTestSecret, sha256.Sum256([]byte("second")),
	); err == nil || !strings.Contains(err.Error(), "invalid_cluster_key") {
		t.Fatalf("another node with the spent key: %v", err)
	}
	if status, err := leader.service.AdmitJoiner(t.Context(), request("first", "127.0.0.2"), "", testJoinPin); err != nil ||
		status.Suffrage != "nonvoter" {
		t.Fatalf("the admitted node asking again: %+v, %v", status, err)
	}

	seedJoinKey(t, leader)
	if err := leader.service.apply("cluster_member", "remove", clusterModels.ClusterMemberRemoval{NodeID: "first"}); err != nil {
		t.Fatal(err)
	}
	if clusterModels.JoinKeyMatches(leader.service.DB, guestIdentityJoinTestSecret) {
		t.Fatal("removing a member left the key usable")
	}
}

func TestIntegrationRaftMembershipStatusAnswersTheNodeItself(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, guestIdentityJoinTestModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	if _, err := leader.service.AdmitJoiner(t.Context(), JoinAdmissionRequest{
		NodeID: "member", NodeIP: "127.0.0.2", NodeVersion: cmd.Version, SSHPublicKey: "ssh-ed25519 AAAA",
		Inventory: BuildGuestIdentityInventoryReport(nil),
	}, guestIdentityJoinTestSecret, testJoinPin); err != nil {
		t.Fatal(err)
	}
	other := sha256.Sum256([]byte("other"))

	status, err := leader.service.AuthoritativeMembershipStatus("member", testJoinPin)
	if err != nil || !status.Present || status.Suffrage != "nonvoter" || status.LeaderID != leader.id {
		t.Fatalf("the member's own status = %+v, %v", status, err)
	}
	if status, err := leader.service.AuthoritativeMembershipStatus("member", other); err == nil ||
		err.Error() != "membership_status_cert_mismatch" || status.Present {
		t.Fatalf("a member's status asked by another cert = %+v, %v", status, err)
	}
	if status, err := leader.service.AuthoritativeMembershipStatus("stranger", other); err != nil || status.Present {
		t.Fatalf("a non-member's status = %+v, %v", status, err)
	}
}

func TestIntegrationRaftMembershipStatusNeedsALeaderThatStillLeads(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, guestIdentityJoinTestModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if status, err := leader.service.AuthoritativeMembershipStatus("stranger", testJoinPin); err != nil || status.Present {
		t.Fatalf("a connected leader answered %+v, %v", status, err)
	}
	for _, node := range nodes {
		if node != leader {
			node.transport.Disconnect(leader.addr)
		}
	}
	leader.transport.DisconnectAll()
	// Let in-flight heartbeats land, as they still count for VerifyLeader; this is well within the lease.
	time.Sleep(30 * time.Millisecond)
	if status, err := leader.service.AuthoritativeMembershipStatus("stranger", testJoinPin); err == nil {
		t.Fatalf("a cut-off leader answered %+v", status)
	}
}

func TestIntegrationRaftAdmitJoinerIsIdempotent(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, guestIdentityJoinTestModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)

	request := JoinAdmissionRequest{
		NodeID: "joining-node", NodeIP: "127.0.0.2", NodeVersion: cmd.Version, SSHPublicKey: "ssh-ed25519 AAAA",
		Inventory: BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
			NodeID: "joining-node", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 400, RecordID: 1, Name: "vm-400",
		}}),
	}
	first, err := leader.service.AdmitJoiner(context.Background(), request, guestIdentityJoinTestSecret, testJoinPin)
	if err != nil || first.Phase != JoinPhaseCatchingUp || first.Suffrage != "nonvoter" ||
		first.Pins["joining-node"] != hex.EncodeToString(testJoinPin[:]) {
		t.Fatalf("first admission = %+v, %v", first, err)
	}
	afterFirst := raftConfigurationForGuestIdentityJoinTest(t, leader)
	second, err := leader.service.AdmitJoiner(context.Background(), request, guestIdentityJoinTestSecret, testJoinPin)
	if err != nil || !reflect.DeepEqual(second, first) {
		t.Fatalf("second admission = %+v, %v; want %+v", second, err, first)
	}
	if afterSecond := raftConfigurationForGuestIdentityJoinTest(t, leader); !reflect.DeepEqual(afterSecond, afterFirst) {
		t.Fatalf("second admission changed membership: first=%+v second=%+v", afterFirst, afterSecond)
	}
	waitForGuestIdentityClaim(t, nodes, 400, "joining-node")

	if node, ok := leader.service.PinNode(testJoinPin); !ok || node != "joining-node" {
		t.Fatalf("joiner's pin maps to %q (%t)", node, ok)
	}
	var identity clusterModels.ClusterSSHIdentity
	if err := leader.service.DB.First(&identity, "node_uuid = ?", "joining-node").Error; err != nil ||
		identity.SSHHost != "127.0.0.2" || identity.PublicKey != "ssh-ed25519 AAAA" {
		t.Fatalf("joiner identity = %+v, %v", identity, err)
	}
	if _, err := leader.service.AdmitJoiner(
		context.Background(), request, guestIdentityJoinTestSecret, sha256.Sum256([]byte("impostor")),
	); err == nil || !strings.Contains(err.Error(), "joining_node_pin_mismatch") {
		t.Fatalf("admission with another pin: %v", err)
	}
	if node, ok := leader.service.PinNode(testJoinPin); !ok || node != "joining-node" {
		t.Fatal("a refused admission changed the joiner's pin")
	}
}

func TestIntegrationRaftAdmitJoinerKeepsAVoter(t *testing.T) {
	nodes, _ := newJoinTestCluster(t, 2)
	leader := waitForClusterRaftLeader(t, nodes[:2], 8*time.Second)
	voter := nodes[0]
	if voter == leader {
		voter = nodes[1]
	}
	before := raftConfigurationForGuestIdentityJoinTest(t, leader)
	request := JoinAdmissionRequest{
		NodeID: voter.id, NodeIP: raftAddressHost(voter.id), NodeVersion: cmd.Version, SSHPublicKey: "ssh-ed25519 AAAA",
		Inventory: BuildGuestIdentityInventoryReport(nil),
	}
	// A keyless re-admission needs the member's recorded pin, and this one has none.
	if _, err := leader.service.AdmitJoiner(t.Context(), request, "", testJoinPin); err == nil ||
		!strings.Contains(err.Error(), "invalid_cluster_key") {
		t.Fatalf("keyless admission of a member without a pin: %v", err)
	}
	status, err := leader.service.AdmitJoiner(t.Context(), request, guestIdentityJoinTestSecret, testJoinPin)
	if err != nil || status.Phase != JoinPhaseComplete || status.Suffrage != "voter" {
		t.Fatalf("admission = %+v, %v", status, err)
	}
	if after := raftConfigurationForGuestIdentityJoinTest(t, leader); !reflect.DeepEqual(after, before) {
		t.Fatalf("admitting a voter again changed membership: before=%+v after=%+v", before, after)
	}
}

func TestIntegrationClearClusteredDataResetsTheAppliedIndex(t *testing.T) {
	models := guestIdentityJoinTestModels()
	nodes := setupClusterRaftTestNodes(t, 1, models...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	seedGuestIdentityJoinTestClaim(
		t,
		leader,
		leader.id,
		clusterModels.ReplicationGuestTypeVM,
		811,
	)
	if leader.service.AppliedIndex() == 0 {
		t.Fatal("applied index = 0, want the claim's")
	}

	if err := leader.service.clearClusteredData(); err != nil {
		t.Fatalf("clear clustered data: %v", err)
	}
	if leader.service.AppliedIndex() != 0 {
		t.Fatalf("applied index after clear = %d, want 0", leader.service.AppliedIndex())
	}
	if digest, err := leader.service.LocalReplicatedStateDigest(leader.id); err != nil || digest.AppliedIndex != 0 {
		t.Fatalf("digest after clear = %+v, %v; want index 0", digest, err)
	}
}

func TestIntegrationClearClusteredDataWaitsForFSMApply(t *testing.T) {
	models := guestIdentityJoinTestModels()
	nodes := setupClusterRaftTestNodes(t, 1, models...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	seedGuestIdentityJoinTestClaim(
		t,
		leader,
		leader.id,
		clusterModels.ReplicationGuestTypeVM,
		812,
	)

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseFSM := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseFSM()
	clusterModels.On(leader.service.fsm, "test_hold_apply", "hold", func(*gorm.DB, *struct{}) error {
		close(started)
		<-release
		return nil
	})
	future := leader.raft.Apply(
		[]byte(`{"version":1,"decidedAt":"2026-09-24T00:00:00Z","type":"test_hold_apply","action":"hold","data":{}}`),
		10*time.Second,
	)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("hold command never reached the FSM")
	}

	// The held entry owns the database transaction, so only the clear's
	// completion can be observed until it's released.
	cleared := make(chan error, 1)
	go func() { cleared <- leader.service.clearClusteredData() }()
	select {
	case err := <-cleared:
		t.Fatalf("clear finished while the FSM lock was held: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	releaseFSM()
	if err := <-cleared; err != nil {
		t.Fatalf("clear clustered data: %v", err)
	}
	if err := future.Error(); err != nil {
		t.Fatalf("hold command apply: %v", err)
	}
	if leader.service.AppliedIndex() != 0 {
		t.Fatalf("applied index after clear = %d, want 0", leader.service.AppliedIndex())
	}
}

func seedJoinKey(t *testing.T, node *clusterRaftTestNode) {
	t.Helper()
	if err := node.service.apply("cluster_join_key", "set", clusterModels.ClusterJoinKey{
		Hash: clusterModels.JoinKeyHash(guestIdentityJoinTestSecret),
	}); err != nil {
		t.Fatalf("seed join key: %v", err)
	}
}

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
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/cmd"
	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

func newJoinTestService(t *testing.T) *Service {
	t.Helper()
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{}, &vmModels.VM{}, &jailModels.Jail{})
	if err := db.Create(&clusterModels.Cluster{RaftPort: ClusterRaftPort}).Error; err != nil {
		t.Fatal(err)
	}
	return withFSM(&Service{DB: db, NodeID: "joining-node", mutationGate: newOpenTestMutationGate(t)})
}

func requireNoJoin(t *testing.T, service *Service) {
	t.Helper()
	record, err := service.loadClusterRecord()
	if err != nil || record.Enabled || record.HasIncompleteJoin() || record.Key != "" || record.RaftIP != "" {
		t.Fatalf("cluster record = %+v, %v; want a standalone node", record, err)
	}
}

// Node IDs are Raft addresses, as the leader derives a joining node's ID from its IP.
func newJoinTestCluster(t *testing.T, size int) ([]*clusterRaftTestNode, *testPeerNetwork) {
	t.Helper()
	if testing.Short() {
		t.Skip("requires real in-memory Raft; covered by the native integration lane")
	}
	nodes := make([]*clusterRaftTestNode, 0, size+1)
	for i := 1; i <= size+1; i++ {
		nodes = append(nodes, newClusterRaftTestNode(t, RaftServerAddress(fmt.Sprintf("127.0.0.%d", i)), guestIdentityJoinTestModels()...))
	}
	members, joiner := nodes[:size], nodes[size]
	connectClusterRaftTestNodes(members)
	if err := members[0].raft.BootstrapCluster(raft.Configuration{Servers: []raft.Server{{
		ID: raft.ServerID(members[0].id), Address: members[0].addr,
	}}}).Error(); err != nil {
		t.Fatal(err)
	}
	waitForClusterRaftLeader(t, members, 8*time.Second)
	for _, member := range members[1:] {
		addClusterRaftVoterWithRetry(t, members, member)
	}
	waitForClusterRaftVoterCount(t, members, size, 8*time.Second)
	for _, member := range members {
		seedGuestIdentityJoinTestCluster(t, member)
		for _, peer := range members {
			if err := member.service.DB.Create(&clusterModels.ClusterNode{
				NodeUUID: peer.id, Hostname: peer.id, Protocol: clusterModels.ClusterProtocol,
			}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := joiner.service.DB.Create(&clusterModels.Cluster{RaftPort: ClusterRaftPort}).Error; err != nil {
		t.Fatal(err)
	}
	joiner.service.joinRaftStarter = func(ip, clusterKey string) error {
		if err := joiner.service.clearClusteredData(); err != nil {
			return err
		}
		if joiner.raft.State() == raft.Shutdown {
			_ = joiner.transport.Close()
			startClusterRaftTestNode(t, joiner)
		}
		record, err := joiner.service.loadClusterRecord()
		if err != nil {
			return err
		}
		if err := joiner.service.DB.Model(&record).Updates(map[string]any{
			"enabled": true, "key": clusterKey, "raft_ip": ip,
		}).Error; err != nil {
			return err
		}
		connectClusterRaftTestNodes(nodes)
		return nil
	}
	return nodes, routeTestPeerRequests(nodes)
}

func joinTestCluster(t *testing.T, joiner, through *clusterRaftTestNode) (ClusterJoinStatus, error) {
	t.Helper()
	return joiner.service.Join(t.Context(), raftAddressHost(through.id), raftAddressHost(joiner.id), guestIdentityJoinTestKey)
}

func reconcileJoinsUntilVoter(t *testing.T, nodes []*clusterRaftTestNode, joiner *clusterRaftTestNode) {
	t.Helper()
	waitForClusterCondition(t, 10*time.Second, "join to finish", func() bool {
		for _, node := range nodes {
			node.service.reconcileJoins(t.Context(), time.Now())
		}
		status, err := joiner.service.JoinStatus()
		record, recordErr := joiner.service.loadClusterRecord()
		return err == nil && recordErr == nil && status.Phase == JoinPhaseComplete && !record.HasIncompleteJoin()
	})
}

func TestJoinRejectsIPv6WithoutPersisting(t *testing.T) {
	service := newJoinTestService(t)
	for _, ips := range [][2]string{{"2001:db8::10", "192.0.2.20"}, {"192.0.2.10", "2001:db8::20"}} {
		if _, err := service.Join(t.Context(), ips[0], ips[1], guestIdentityJoinTestKey); err == nil || err.Error() != "cluster_ipv6_unsupported" {
			t.Fatalf("join %v error = %v", ips, err)
		}
	}
	requireNoJoin(t, service)
}

func TestJoinRefusesWhileAGuestIDIsInFlight(t *testing.T) {
	service := newJoinTestService(t)
	reservation, err := service.ReserveGuestIdentities(t.Context(), clusterModels.ReplicationGuestTypeVM, []uint{45})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Join(t.Context(), "192.0.2.10", "192.0.2.20", guestIdentityJoinTestKey); err == nil ||
		err.Error() != "guest_identity_mutation_in_progress" {
		t.Fatalf("join error = %v", err)
	}
	requireNoJoin(t, service)
	if err := service.ReleaseGuestIdentities(t.Context(), reservation); err != nil {
		t.Fatal(err)
	}
}

func TestJoinKeepsItsIntentUnlessRefused(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		err     error
		refused bool
	}{
		{name: "unreachable", err: errors.New("dial tcp: connection refused")},
		{name: "unavailable", status: http.StatusServiceUnavailable},
		{name: "fenced", status: http.StatusLocked},
		{name: "refused", status: http.StatusConflict, refused: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newJoinTestService(t)
			service.peerRequest = func(context.Context, string, string, []byte, map[string]string) (int, []byte, error) {
				body, err := json.Marshal(internal.APIResponse[any]{Status: "error", Message: "test_" + test.name})
				if err != nil {
					return 0, nil, err
				}
				return test.status, body, test.err
			}
			service.joinRaftStarter = func(string, string) error {
				t.Error("Raft started without an admission")
				return nil
			}

			status, err := service.Join(t.Context(), "192.0.2.10", "192.0.2.20", guestIdentityJoinTestKey)
			if test.refused {
				var refused *JoinAdmissionError
				if !errors.As(err, &refused) || refused.StatusCode != test.status {
					t.Fatalf("join error = %v, want a refusal", err)
				}
				requireNoJoin(t, service)
				return
			}
			if err != nil || status.Phase != JoinPhaseSubmitting || !status.Retrying || status.LastError == "" || status.Attempts != 1 {
				t.Fatalf("join = %+v, %v; want one that retries", status, err)
			}
			record, err := service.loadClusterRecord()
			if err != nil || record.JoinLeaderIP != "192.0.2.10" || record.RaftIP != "192.0.2.20" || record.Key != guestIdentityJoinTestKey || record.Enabled {
				t.Fatalf("cluster record = %+v, %v", record, err)
			}
		})
	}
}

func TestJoinRefusesAClusteredNode(t *testing.T) {
	service := newJoinTestService(t)
	if err := service.DB.Model(&clusterModels.Cluster{}).Where("1 = 1").Updates(map[string]any{
		"enabled": true, "key": guestIdentityJoinTestKey, "raft_ip": "192.0.2.20",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Join(t.Context(), "192.0.2.10", "192.0.2.20", guestIdentityJoinTestKey); err == nil || err.Error() != "clustered_already" {
		t.Fatalf("join error = %v", err)
	}
}

func TestIntegrationJoinThroughAFollowerEndsAsVoter(t *testing.T) {
	nodes, _ := newJoinTestCluster(t, 2)
	members, joiner := nodes[:2], nodes[2]
	leader := waitForClusterRaftLeader(t, members, 8*time.Second)
	follower := members[0]
	if follower == leader {
		follower = members[1]
	}
	if err := leader.service.ProposeNoteCreate(t.Context(), "cluster-note", "kept"); err != nil {
		t.Fatal(err)
	}
	if err := joiner.service.DB.Create(&clusterModels.ClusterNote{Title: "standalone-note"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := joiner.service.DB.Create(&vmModels.VM{RID: 300, Name: "joiner-vm"}).Error; err != nil {
		t.Fatal(err)
	}

	status, err := joinTestCluster(t, joiner, follower)
	if err != nil || status.Phase != JoinPhaseCatchingUp || status.LastError != "" {
		t.Fatalf("join = %+v, %v", status, err)
	}
	if record, err := joiner.service.loadClusterRecord(); err != nil || record.JoinLeaderIP != raftAddressHost(follower.id) {
		t.Fatalf("joined through %q, %v; want the member it asked, %s", record.JoinLeaderIP, err, raftAddressHost(follower.id))
	}
	reconcileJoinsUntilVoter(t, nodes, joiner)
	waitForClusterRaftVoterCount(t, nodes, 3, 8*time.Second)
	waitForGuestIdentityClaim(t, nodes, 300, joiner.id)
	var notes []clusterModels.ClusterNote
	if err := joiner.service.DB.Find(&notes).Error; err != nil || len(notes) != 1 || notes[0].Title != "cluster-note" {
		t.Fatalf("joiner notes = %+v, %v; want only the cluster's", notes, err)
	}
}

func TestIntegrationRefusedJoinLeavesTheNodeUntouched(t *testing.T) {
	nodes, _ := newJoinTestCluster(t, 1)
	leader, joiner := nodes[0], nodes[1]
	seedGuestIdentityJoinTestClaim(t, leader, leader.id, clusterModels.ReplicationGuestTypeVM, 300)
	if err := joiner.service.DB.Create(&vmModels.VM{RID: 300, Name: "clashing-vm"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := joiner.service.DB.Create(&clusterModels.ClusterNote{Title: "standalone-note"}).Error; err != nil {
		t.Fatal(err)
	}
	joiner.service.joinRaftStarter = func(string, string) error {
		t.Error("Raft started for a refused join")
		return nil
	}

	_, err := joinTestCluster(t, joiner, leader)
	var refused *JoinAdmissionError
	if !errors.As(err, &refused) || refused.StatusCode != http.StatusConflict || refused.Message != "guest_identity_inventory_conflict" {
		t.Fatalf("join error = %v, want the conflict", err)
	}
	var report GuestIdentityInventoryReport
	if err := json.Unmarshal(refused.Data, &report); err != nil || len(report.Conflicts) != 1 || report.Conflicts[0].GuestID != 300 {
		t.Fatalf("conflict report = %+v, %v", report, err)
	}
	requireNoJoin(t, joiner.service)
	var notes []clusterModels.ClusterNote
	if err := joiner.service.DB.Find(&notes).Error; err != nil || len(notes) != 1 || notes[0].Title != "standalone-note" {
		t.Fatalf("joiner notes = %+v, %v", notes, err)
	}
	if servers := raftConfigurationForGuestIdentityJoinTest(t, leader).Servers; len(servers) != 1 {
		t.Fatalf("leader configuration = %+v", servers)
	}
}

func TestIntegrationJoinRefusesLocalUsersTheClusterHas(t *testing.T) {
	for _, origin := range []string{"", "another-cluster"} {
		t.Run("origin="+origin, func(t *testing.T) {
			nodes, _ := newJoinTestCluster(t, 1)
			leader, joiner := nodes[0], nodes[1]
			if err := leader.service.DB.Create(&clusterModels.SylveUser{Username: "alice", Password: "hash-cluster"}).Error; err != nil {
				t.Fatal(err)
			}
			for _, value := range []any{
				&clusterModels.SylveUser{Username: "alice", Password: "hash-here"},
				&models.User{Username: "alice", Password: "hash-here", Source: "local", Shared: true, SharedClusterID: origin, SharedUserID: 1},
				&models.User{Username: "admin", Password: "hash-admin", Source: "local"},
			} {
				if err := joiner.service.DB.Create(value).Error; err != nil {
					t.Fatal(err)
				}
			}
			joiner.service.joinRaftStarter = func(string, string) error {
				t.Error("Raft started for a refused join")
				return nil
			}

			_, err := joinTestCluster(t, joiner, leader)
			var refused *JoinAdmissionError
			if !errors.As(err, &refused) || refused.StatusCode != http.StatusConflict || refused.Detail != "joining_node_users_taken: alice" {
				t.Fatalf("join error = %v, want alice taken", err)
			}
			requireNoJoin(t, joiner.service)
			var alice models.User
			if err := joiner.service.DB.Where("username = ?", "alice").First(&alice).Error; err != nil ||
				!alice.Shared || alice.Password != "hash-here" || alice.SharedClusterID != origin {
				t.Fatalf("joiner alice = %+v, %v; want it untouched", alice, err)
			}
		})
	}
}

func recordCurrentProtocol(t *testing.T, leader, node *clusterRaftTestNode) {
	t.Helper()
	if err := leader.service.DB.Where("node_uuid = ?", node.id).
		Assign(clusterModels.ClusterNode{Protocol: clusterModels.ClusterProtocol}).
		FirstOrCreate(&clusterModels.ClusterNode{NodeUUID: node.id}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationRejoinAfterTipUpgradeRemoval(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	nodes, _ := newJoinTestCluster(t, 2)
	leader := waitForClusterRaftLeader(t, nodes[:2], 8*time.Second)
	joiner := nodes[2]
	if _, err := joinTestCluster(t, joiner, leader); err != nil {
		t.Fatal(err)
	}
	reconcileJoinsUntilVoter(t, nodes, joiner)
	// Published tip resumes this Raft format with neither an ID nor shared users.
	if id, err := clusterModels.ReadClusterID(leader.service.DB); err != nil || id != "" {
		t.Fatalf("pre-upgrade identity = %q, %v", id, err)
	}
	recordCurrentProtocol(t, leader, joiner)
	if err := leader.service.Propose(t.Context(), "sylve_user", "create", clusterModels.SylveUser{
		Username: "alice", Password: "hash-alice",
	}); err != nil {
		t.Fatal(err)
	}
	var alice models.User
	waitForClusterCondition(t, time.Second, "shared user replicated", func() bool {
		return joiner.service.DB.Where("username = ?", "alice").First(&alice).Error == nil && alice.Shared
	})
	// A removed, partitioned node learns of its departure through the API alone.
	joiner.transport.DisconnectAll()
	for _, node := range nodes[:2] {
		node.transport.Disconnect(joiner.addr)
	}
	if err := leader.service.RemoveMember(t.Context(), joiner.id, true); err != nil {
		t.Fatal(err)
	}
	reconcileUntilLeft(t, joiner)
	if err := joiner.service.mutationGate.Open(); err != nil {
		t.Fatal(err)
	}
	seedJoinKey(t, leader)
	if status, err := joinTestCluster(t, joiner, leader); err != nil || status.LastError != "" {
		t.Fatalf("removed member rejoin = %+v, %v", status, err)
	}
	reconcileJoinsUntilVoter(t, nodes, joiner)
	var current models.User
	if err := joiner.service.DB.Where("username = ?", "alice").First(&current).Error; err != nil ||
		current.ID != alice.ID || !current.Shared || current.SharedClusterID == "" || current.SharedUserID == 0 {
		t.Fatalf("returning user = %+v, %v", current, err)
	}
}

func TestIntegrationRejoinReconcilesSharedUsers(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	nodes, _ := newJoinTestCluster(t, 1)
	leader, joiner := nodes[0], nodes[1]
	for _, name := range []string{"alice", "gone", "renamed", "replaced"} {
		if err := leader.service.Propose(t.Context(), "sylve_user", "create", clusterModels.SylveUser{
			Username: name, Password: "hash-old",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := joinTestCluster(t, joiner, leader); err != nil {
		t.Fatal(err)
	}
	reconcileJoinsUntilVoter(t, nodes, joiner)
	var alice models.User
	if err := joiner.service.DB.Where("username = ?", "alice").First(&alice).Error; err != nil {
		t.Fatal(err)
	}
	if err := joiner.service.DB.Create(&models.Token{UserID: alice.ID, Token: "old-session"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gone", "renamed", "replaced"} {
		var user models.User
		if err := joiner.service.DB.Where("username = ?", name).First(&user).Error; err != nil {
			t.Fatal(err)
		}
		if err := joiner.service.DB.Create(&models.WebAuthnCredential{
			UserID: user.ID, CredentialID: name + "-key", Data: []byte("x"),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	recordCurrentProtocol(t, leader, joiner)
	if err := joiner.service.Leave(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	requireLeft(t, joiner)
	if err := joiner.service.mutationGate.Open(); err != nil {
		t.Fatal(err)
	}
	if err := leader.service.Propose(t.Context(), "sylve_user", "update", clusterModels.SylveUser{
		Username: "alice", Password: "hash-current", Email: "current@example.com", Locked: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := leader.service.Propose(t.Context(), "sylve_user", "delete", clusterModels.SylveUser{Username: "gone"}); err != nil {
		t.Fatal(err)
	}
	if err := leader.service.Propose(t.Context(), "sylve_user", "delete", clusterModels.SylveUser{Username: "replaced"}); err != nil {
		t.Fatal(err)
	}
	if err := leader.service.Propose(t.Context(), "sylve_user", "create", clusterModels.SylveUser{
		Username: "replaced", Password: "hash-replacement",
	}); err != nil {
		t.Fatal(err)
	}
	if err := leader.service.Propose(t.Context(), "sylve_user", "rename", clusterModels.SylveUserRename{
		From: "renamed", To: "new-name",
	}); err != nil {
		t.Fatal(err)
	}
	if err := joiner.service.Propose(t.Context(), "sylve_user", "update", clusterModels.SylveUser{
		Username: "alice", Password: "hash-standalone",
	}); err != nil {
		t.Fatal(err)
	}
	if err := joiner.service.Propose(t.Context(), "sylve_user", "create", clusterModels.SylveUser{
		Username: "fresh", Password: "hash-fresh",
	}); err != nil {
		t.Fatal(err)
	}
	if err := joiner.service.Propose(t.Context(), "acl_grant", "set", clusterModels.ACLGrant{
		Path: "/", SubjectType: clusterModels.ACLSubjectUser, Subject: "fresh@sylve", Role: "Administrator", Propagate: true,
	}); err != nil {
		t.Fatal(err)
	}
	seedJoinKey(t, leader)
	// Force snapshot catch-up, as a fresh Raft store must handle changes made while away.
	if err := leader.service.snapshotPreClusterState(); err != nil {
		t.Fatal(err)
	}
	start := joiner.service.joinRaftStarter
	joiner.service.joinRaftStarter = func(string, string) error {
		if err := joiner.service.clearClusteredData(); err != nil {
			return err
		}
		return errors.New("interrupted_after_clear")
	}
	status, err := joinTestCluster(t, joiner, leader)
	if err != nil || status.LastError != "interrupted_after_clear" {
		t.Fatalf("rejoin = %+v, %v", status, err)
	}
	joiner.service.joinRaftStarter = start
	reconcileJoinsUntilVoter(t, nodes, joiner)
	var current models.User
	if err := joiner.service.DB.Where("username = ?", "alice").First(&current).Error; err != nil ||
		current.ID != alice.ID || !current.Shared || current.Password != "hash-current" || !current.Locked || current.Email != "current@example.com" {
		t.Fatalf("returning alice = %+v, %v; want the current cluster account", current, err)
	}
	for _, name := range []string{"gone", "renamed"} {
		if err := joiner.service.DB.Where("username = ?", name).First(&models.User{}).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("old user %s survived rejoin: %v", name, err)
		}
	}
	for name, password := range map[string]string{"new-name": "hash-old", "replaced": "hash-replacement"} {
		var user models.User
		if err := joiner.service.DB.Where("username = ?", name).First(&user).Error; err != nil || !user.Shared || user.Password != password {
			t.Fatalf("cluster user %s = %+v, %v", name, user, err)
		}
	}
	for _, model := range []any{&models.Token{}, &models.WebAuthnCredential{}, &clusterModels.ACLGrant{}} {
		var count int64
		if err := joiner.service.DB.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%T after rejoin = %d, %v", model, count, err)
		}
	}
	var fresh models.User
	if err := joiner.service.DB.Where("username = ?", "fresh").First(&fresh).Error; err != nil || fresh.Password != "hash-fresh" {
		t.Fatalf("standalone-created user = %+v, %v; want it retained", fresh, err)
	}
	recordCurrentProtocol(t, leader, joiner)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	service := auth.NewAuthService(joiner.service.DB).(*auth.Service)
	service.SetProposer(func(ctx context.Context, commandType, action string, payload any) error {
		defer cancel()
		return joiner.service.Propose(ctx, commandType, action, payload)
	})
	service.ShareLocalUsers(ctx)
	var names []string
	if err := leader.service.DB.Model(&clusterModels.SylveUser{}).Order("username").Pluck("username", &names).Error; err != nil ||
		!slices.Equal(names, []string{"alice", "fresh", "new-name", "replaced"}) {
		t.Fatalf("shared users after rejoin = %v, %v", names, err)
	}
}

func TestIntegrationJoinResumesAfterAnInterruptedStart(t *testing.T) {
	nodes, _ := newJoinTestCluster(t, 1)
	leader, joiner := nodes[0], nodes[1]
	if err := joiner.service.DB.Create(&vmModels.VM{RID: 300, Name: "joiner-vm"}).Error; err != nil {
		t.Fatal(err)
	}
	start := joiner.service.joinRaftStarter
	joiner.service.joinRaftStarter = func(string, string) error { return errors.New("raft_start_interrupted") }

	status, err := joinTestCluster(t, joiner, leader)
	if err != nil || status.Phase != JoinPhaseSubmitting || !strings.Contains(status.LastError, "raft_start_interrupted") {
		t.Fatalf("join = %+v, %v; want one to go on with", status, err)
	}
	waitForGuestIdentityClaim(t, []*clusterRaftTestNode{leader}, 300, joiner.id)
	if _, err := joiner.service.ReserveGuestIdentities(t.Context(), clusterModels.ReplicationGuestTypeJail, []uint{301}); err == nil ||
		!strings.Contains(err.Error(), "cluster_formation_in_progress") {
		t.Fatalf("guest creation during the join: %v", err)
	}

	joiner.service.joinRaftStarter = start
	reconcileJoinsUntilVoter(t, nodes, joiner)
	waitForGuestIdentityClaim(t, nodes, 300, joiner.id)
}

func TestIntegrationGoneJoinerIsRemovedAndComesBackStandalone(t *testing.T) {
	nodes, network := newJoinTestCluster(t, 1)
	leader, joiner := nodes[0], nodes[1]
	if err := joiner.service.DB.Create(&vmModels.VM{RID: 300, Name: "joiner-vm"}).Error; err != nil {
		t.Fatal(err)
	}
	start := joiner.service.joinRaftStarter
	joiner.service.joinRaftStarter = func(ip, clusterKey string) error {
		if err := start(ip, clusterKey); err != nil {
			return err
		}
		joiner.transport.DisconnectAll()
		leader.transport.Disconnect(joiner.addr)
		network.setCut(joiner.id, true)
		return nil
	}
	if _, err := joinTestCluster(t, joiner, leader); err != nil {
		t.Fatal(err)
	}
	waitForGuestIdentityClaim(t, []*clusterRaftTestNode{leader}, 300, joiner.id)

	now := time.Now()
	leader.service.promoteNonvoters(t.Context(), now)
	leader.service.promoteNonvoters(t.Context(), now.Add(nonvoterAbandonAfter))
	if servers := raftConfigurationForGuestIdentityJoinTest(t, leader).Servers; len(servers) != 1 {
		t.Fatalf("leader configuration after giving up = %+v", servers)
	}
	waitForGuestIdentityClaim(t, []*clusterRaftTestNode{leader}, 300, "")

	connectClusterRaftTestNodes(nodes)
	network.setCut(joiner.id, false)
	waitForClusterCondition(t, 10*time.Second, "joiner to run standalone", func() bool {
		joiner.service.reconcileJoins(t.Context(), time.Now())
		record, err := joiner.service.loadClusterRecord()
		return err == nil && !record.Enabled && !record.HasIncompleteJoin()
	})
	var vm vmModels.VM
	if err := joiner.service.DB.First(&vm, "rid = ?", 300).Error; err != nil {
		t.Fatalf("joiner lost its guest: %v", err)
	}
	waitForGuestIdentityClaim(t, []*clusterRaftTestNode{leader}, 300, "")
}

func TestIntegrationPromoterWaitsOnASlowNonvoter(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, guestIdentityJoinTestModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	leader.service.AuthService = &guestIdentityInventoryAuthStub{}
	if _, err := leader.service.AdmitJoiner(t.Context(), JoinAdmissionRequest{
		NodeID: "slow-node", NodeIP: "127.0.0.9", NodeVersion: cmd.Version, SSHPublicKey: "ssh-ed25519 AAAA",
		Inventory: BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
			NodeID: "slow-node", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 500, RecordID: 1, Name: "vm-500",
		}}),
	}, guestIdentityJoinTestSecret, testJoinPin); err != nil {
		t.Fatal(err)
	}
	var applied atomic.Uint64
	leader.service.peerRequest = func(context.Context, string, string, []byte, map[string]string) (int, []byte, error) {
		progress, err := json.Marshal(ClusterJoinProgress{NodeID: "slow-node", AppliedIndex: applied.Load()})
		if err != nil {
			return 0, nil, err
		}
		body, err := json.Marshal(internal.APIResponse[CallResult]{Status: "success", Data: CallResult{Data: progress}})
		return http.StatusOK, body, err
	}
	present := func() bool {
		for _, server := range raftConfigurationForGuestIdentityJoinTest(t, leader).Servers {
			if server.ID == "slow-node" {
				return true
			}
		}
		return false
	}

	start := time.Now()
	applied.Store(1)
	leader.service.promoteNonvoters(t.Context(), start)
	applied.Store(2)
	leader.service.promoteNonvoters(t.Context(), start.Add(nonvoterAbandonAfter-time.Minute))
	leader.service.promoteNonvoters(t.Context(), start.Add(nonvoterAbandonAfter+time.Minute))
	if !present() {
		t.Fatal("a nonvoter that kept applying was given up on")
	}
	leader.service.promoteNonvoters(t.Context(), start.Add(2*nonvoterAbandonAfter))
	if present() {
		t.Fatal("a nonvoter that stopped applying was kept")
	}
	waitForGuestIdentityClaim(t, nodes, 500, "")
}

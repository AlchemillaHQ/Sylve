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
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/cmd"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/google/uuid"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

func TestInitializeReaddressRuntimeNormalizesCommittedBindAndFences(t *testing.T) {
	database := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	if err := database.Create(&clusterModels.Cluster{
		Enabled: true, RaftIP: "192.0.2.10", ReaddressOldIP: "192.0.2.10",
		ReaddressNewIP: "192.0.2.20", ReaddressPhase: ReaddressPhaseMembershipCommitted,
	}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: database, mutationGate: newOpenTestMutationGate(t)}
	if err := service.InitializeReaddressRuntime(); err != nil {
		t.Fatal(err)
	}
	var record clusterModels.Cluster
	if err := database.First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.RaftIP != "192.0.2.20" || record.ReaddressPhase != ReaddressPhaseLocalRebound {
		t.Fatalf("unexpected readdress state: %+v", record)
	}
	if _, _, err := service.EnterMutation(context.Background()); !errors.Is(err, ErrNodeReaddressFenced) {
		t.Fatalf("gate error = %v", err)
	}
	if _, _, err := service.EnterMutation(context.Background()); !errors.Is(err, ErrNodeLeaveFenced) {
		t.Fatalf("readdress fence must remain compatible with generic lifecycle fencing: %v", err)
	}
}

func TestNormalizeReaddressIPRejectsIPv6(t *testing.T) {
	if _, err := normalizeReaddressIP("2001:db8::20"); err == nil || err.Error() != "cluster_readdress_ipv6_unsupported" {
		t.Fatalf("IPv6 error = %v", err)
	}
	if _, err := normalizeReaddressIP("127.0.0.1"); err == nil || err.Error() != "cluster_readdress_ip_invalid" {
		t.Fatalf("loopback error = %v", err)
	}
	if got, err := normalizeReaddressIP("192.0.2.20"); err != nil || got != "192.0.2.20" {
		t.Fatalf("IPv4 = %q, error = %v", got, err)
	}
}

func TestRaftAddressOverrideRequiresDisruptionAcknowledgement(t *testing.T) {
	service := &Service{addressProvider: newRaftAddressProvider()}
	nodeID := raft.ServerID("node-2")
	address := raft.ServerAddress("192.0.2.20:8180")
	if err := service.installRaftAddressOverride(nodeID, address, false); err == nil {
		t.Fatal("expected acknowledgement failure")
	}
	if _, err := service.addressProvider.ServerAddr(nodeID); err == nil {
		t.Fatal("override was installed without acknowledgement")
	}
	if err := service.installRaftAddressOverride(nodeID, address, true); err != nil {
		t.Fatal(err)
	}
	if got, err := service.addressProvider.ServerAddr(nodeID); err != nil || got != address {
		t.Fatalf("override = %q, err = %v", got, err)
	}
	service.clearRaftAddressOverride(nodeID)
}

func TestLeaveCannotOpenOrReplaceReaddressFence(t *testing.T) {
	database := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	if err := database.Create(&clusterModels.Cluster{
		Enabled: true, RaftIP: "192.0.2.20", ReaddressOldIP: "192.0.2.10",
		ReaddressNewIP: "192.0.2.20", ReaddressPhase: ReaddressPhaseLocalRebound,
	}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: database, NodeID: "node-1", mutationGate: NewMutationGate()}
	if _, err := service.prepareLocalLeave(context.Background(), uuid.NewString(), "", false, true); err == nil ||
		err.Error() != "cluster_readdress_in_progress" {
		t.Fatalf("leave error = %v", err)
	}
	if _, _, err := service.EnterMutation(context.Background()); !errors.Is(err, ErrNodeReaddressFenced) {
		t.Fatalf("gate error = %v", err)
	}
}

func TestValidateRecoveryIdentity(t *testing.T) {
	identity := ReaddressIdentity{
		NodeID: "node-2", Enabled: true, OldIP: "192.0.2.10", NewIP: "192.0.2.20",
		RaftIP: "192.0.2.20", Phase: ReaddressPhaseLocalRebound, SylveVersion: cmd.Version,
	}
	if err := validateRecoveryIdentity(identity, "node-2", "192.0.2.10", "192.0.2.20"); err != nil {
		t.Fatal(err)
	}
	identity.NodeID = "node-3"
	if err := validateRecoveryIdentity(identity, "node-2", "192.0.2.10", "192.0.2.20"); err == nil {
		t.Fatal("expected identity mismatch")
	}
}

func TestCommitMemberAddressUpdatesExistingVoterWithoutChangingNodeID(t *testing.T) {
	database := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	if err := database.Create(&clusterModels.Cluster{
		Enabled: true, RaftIP: "192.0.2.20", ReaddressOldIP: "192.0.2.10",
		ReaddressNewIP: "192.0.2.20", ReaddressPhase: ReaddressPhaseLocalRebound,
	}).Error; err != nil {
		t.Fatal(err)
	}
	fsm := clusterModels.NewFSMDispatcher(database)
	clusterModels.RegisterDefaultHandlers(fsm)
	configuration := raft.DefaultConfig()
	configuration.LocalID = "node-1"
	configuration.Logger = hclog.NewNullLogger()
	configuration.HeartbeatTimeout = 100 * time.Millisecond
	configuration.ElectionTimeout = 100 * time.Millisecond
	configuration.LeaderLeaseTimeout = 50 * time.Millisecond
	configuration.CommitTimeout = 10 * time.Millisecond
	oldAddress := raft.ServerAddress("192.0.2.10:8180")
	_, transport := raft.NewInmemTransport(oldAddress)
	instance, err := raft.NewRaft(
		configuration,
		fsm,
		raft.NewInmemStore(),
		raft.NewInmemStore(),
		raft.NewInmemSnapshotStore(),
		transport,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = instance.Shutdown().Error()
		_ = transport.Close()
	})
	if err := instance.BootstrapCluster(raft.Configuration{Servers: []raft.Server{{
		ID: "node-1", Address: oldAddress, Suffrage: raft.Voter,
	}}}).Error(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for instance.State() != raft.Leader && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if instance.State() != raft.Leader {
		t.Fatal("single voter did not elect itself")
	}

	service := &Service{DB: database, Raft: instance, NodeID: "node-1", mutationGate: NewMutationGate()}
	err = service.commitMemberAddress(context.Background(), MemberAddressChangeRequest{
		NodeID: "node-1", OldIP: "192.0.2.10", NewIP: "192.0.2.20", AllowDisruption: true,
	}, "node-1", false)
	if err != nil {
		t.Fatal(err)
	}
	future := instance.GetConfiguration()
	if err := future.Error(); err != nil {
		t.Fatal(err)
	}
	server, present, err := resolveRaftMember(future.Configuration(), "node-1")
	if err != nil || !present {
		t.Fatalf("member present=%v err=%v", present, err)
	}
	if server.ID != "node-1" || server.Address != "192.0.2.20:8180" || server.Suffrage != raft.Voter {
		t.Fatalf("server = %+v", server)
	}
	completed, err := service.completeLocalReaddressIfCommitted()
	if err != nil || !completed {
		t.Fatalf("completed=%v err=%v", completed, err)
	}
	if _, release, err := service.EnterMutation(context.Background()); err != nil {
		t.Fatalf("mutation gate did not reopen: %v", err)
	} else {
		release()
	}
	var record clusterModels.Cluster
	if err := database.First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.ReaddressPhase != "" || record.ReaddressOldIP != "" || record.ReaddressNewIP != "" {
		t.Fatalf("pending state was not cleared: %+v", record)
	}
}

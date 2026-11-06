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
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/cmd"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

func TestInitializeReaddressRuntimeFencesAPendingReaddress(t *testing.T) {
	database := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	if err := database.Create(&clusterModels.Cluster{
		Enabled: true, RaftIP: "192.0.2.20", ReaddressOldIP: "192.0.2.10",
		ReaddressNewIP: "192.0.2.20", ReaddressPhase: ReaddressPhaseLocalRebound,
	}).Error; err != nil {
		t.Fatal(err)
	}
	service := withFSM(&Service{DB: database, mutationGate: newOpenTestMutationGate(t)})
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
	service := withFSM(&Service{DB: database, NodeID: "node-1", mutationGate: newOpenTestMutationGate(t)})
	_, release, err := service.EnterMutation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	service.mutationGate.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := service.Leave(ctx, false); err == nil || err.Error() != "cluster_readdress_in_progress" {
		t.Fatalf("leave error = %v", err)
	}
	release()
	if _, _, err := service.EnterMutation(context.Background()); !errors.Is(err, ErrNodeReaddressFenced) {
		t.Fatalf("gate error = %v", err)
	}
}

func TestValidateRecoveryIdentity(t *testing.T) {
	identity := ReaddressIdentity{
		NodeID: "node-2", Enabled: true, OldIP: "192.0.2.10", NewIP: "192.0.2.20",
		RaftIP: "192.0.2.20", Phase: ReaddressPhaseLocalRebound, SylveVersion: cmd.Version,
	}
	if err := validateRecoveryIdentity(identity, "node-2", "192.0.2.20"); err != nil {
		t.Fatal(err)
	}
	identity.OldIP = "192.0.2.99"
	if err := validateRecoveryIdentity(identity, "node-2", "192.0.2.20"); err != nil {
		t.Fatalf("old IP compared: %v", err)
	}
	identity.NodeID = "node-3"
	if err := validateRecoveryIdentity(identity, "node-2", "192.0.2.20"); err == nil {
		t.Fatal("expected identity mismatch")
	}
}

func TestReconcileSoleMemberReaddressUpdatesExistingVoterAndReopens(t *testing.T) {
	instance, service, database := soleMemberReaddress(t, "192.0.2.10:8180")
	assertSoleMemberReaddressed(t, instance, service, database)
}

// recover-ip redirected this readdress to .20 after .30 was committed and lost,
// so the cluster has the node at .30 while its row says it moves from .10.
func TestReaddressLandsWhenTheClusterHasTheNodeElsewhere(t *testing.T) {
	instance, service, database := soleMemberReaddress(t, "192.0.2.30:8180")
	assertSoleMemberReaddressed(t, instance, service, database)
}

func soleMemberReaddress(t *testing.T, address raft.ServerAddress) (*raft.Raft, *Service, *gorm.DB) {
	t.Helper()
	database := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	if err := database.Create(&clusterModels.Cluster{
		Enabled: true, RaftIP: "192.0.2.20", ReaddressOldIP: "192.0.2.10",
		ReaddressNewIP: "192.0.2.20", ReaddressPhase: ReaddressPhaseLocalRebound,
	}).Error; err != nil {
		t.Fatal(err)
	}
	service := withFSM(&Service{DB: database, NodeID: "node-1", mutationGate: NewMutationGate()})
	return startSoleVoter(t, service, address), service, database
}

func assertSoleMemberReaddressed(t *testing.T, instance *raft.Raft, service *Service, database *gorm.DB) {
	t.Helper()
	service.reconcileReaddress(context.Background())
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

// Two voters: node-1 already listens on its new IP while both configurations
// still name the old one, so node-2 can't reach it there. Committing the new
// address must still work, because node-1 is reachable where it's moving to.
func TestReaddressTwoVotersCommitsAfterRebind(t *testing.T) {
	oldAddress := raft.ServerAddress("192.0.2.11:8180")
	newAddress := raft.ServerAddress("192.0.2.21:8180")
	peerAddress := raft.ServerAddress("192.0.2.12:8180")
	configuration := raft.Configuration{Servers: []raft.Server{
		{ID: "node-1", Address: oldAddress, Suffrage: raft.Voter},
		{ID: "node-2", Address: peerAddress, Suffrage: raft.Voter},
	}}
	start := func(id raft.ServerID, address raft.ServerAddress, database *gorm.DB) (*raft.Raft, *raft.InmemTransport) {
		fsm := clusterModels.NewFSMDispatcher(database)
		clusterModels.RegisterDefaultHandlers(fsm)
		config := raft.DefaultConfig()
		config.LocalID = id
		config.Logger = hclog.NewNullLogger()
		config.HeartbeatTimeout = 100 * time.Millisecond
		config.ElectionTimeout = 100 * time.Millisecond
		config.LeaderLeaseTimeout = 50 * time.Millisecond
		config.CommitTimeout = 10 * time.Millisecond
		_, transport := raft.NewInmemTransport(address)
		instance, err := raft.NewRaft(config, fsm, raft.NewInmemStore(), raft.NewInmemStore(), raft.NewInmemSnapshotStore(), transport)
		if err != nil {
			t.Fatal(err)
		}
		if err := instance.BootstrapCluster(configuration).Error(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = instance.Shutdown().Error()
			_ = transport.Close()
		})
		return instance, transport
	}

	database := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	if err := database.Create(&clusterModels.Cluster{
		Enabled: true, RaftIP: "192.0.2.21", ReaddressOldIP: "192.0.2.11",
		ReaddressNewIP: "192.0.2.21", ReaddressPhase: ReaddressPhaseLocalRebound,
	}).Error; err != nil {
		t.Fatal(err)
	}
	moved, movedTransport := start("node-1", newAddress, database)
	peer, peerTransport := start("node-2", peerAddress, newClusterServiceTestDB(t))
	movedTransport.Connect(peerAddress, peerTransport)
	peerTransport.Connect(newAddress, movedTransport)

	gate := NewMutationGate()
	gate.Close()
	service := withRaft(&Service{
		DB: database, NodeID: "node-1", mutationGate: gate,
		joinVersionForNode: func(context.Context, raft.Server) (string, error) { return cmd.Version, nil },
	}, moved)

	// Only node-1 can reach both voters, so it's the one that gets elected.
	deadline := time.Now().Add(5 * time.Second)
	for moved.State() != raft.Leader && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if moved.State() != raft.Leader {
		t.Fatal("rebound node was not elected")
	}
	if !service.reconcileReaddress(context.Background()) {
		t.Fatal("reconcile found no readdress to drive")
	}

	for _, instance := range []*raft.Raft{moved, peer} {
		future := instance.GetConfiguration()
		if err := future.Error(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			server, present, _ := resolveRaftMember(future.Configuration(), "node-1")
			if present && server.Address == newAddress {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("membership on %s = %+v", instance.String(), future.Configuration().Servers)
			}
			time.Sleep(20 * time.Millisecond)
			future = instance.GetConfiguration()
		}
	}
	var record clusterModels.Cluster
	if err := database.First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.ReaddressPhase != "" {
		t.Fatalf("readdress still pending: %+v", record)
	}
	if _, release, err := service.EnterMutation(context.Background()); err != nil {
		t.Fatalf("node was not reopened: %v", err)
	} else {
		release()
	}
}

// overrideTransport dials through the service's address overrides, as the
// production NetworkTransport does through its ServerAddressProvider.
type overrideTransport struct {
	*raft.InmemTransport
	provider *raftAddressProvider
}

func (t *overrideTransport) target(id raft.ServerID, target raft.ServerAddress) raft.ServerAddress {
	if address, err := t.provider.ServerAddr(id); err == nil {
		return address
	}
	return target
}

func (t *overrideTransport) AppendEntriesPipeline(id raft.ServerID, target raft.ServerAddress) (raft.AppendPipeline, error) {
	return t.InmemTransport.AppendEntriesPipeline(id, t.target(id, target))
}

func (t *overrideTransport) AppendEntries(id raft.ServerID, target raft.ServerAddress, args *raft.AppendEntriesRequest, resp *raft.AppendEntriesResponse) error {
	return t.InmemTransport.AppendEntries(id, t.target(id, target), args, resp)
}

func (t *overrideTransport) RequestVote(id raft.ServerID, target raft.ServerAddress, args *raft.RequestVoteRequest, resp *raft.RequestVoteResponse) error {
	return t.InmemTransport.RequestVote(id, t.target(id, target), args, resp)
}

func (t *overrideTransport) RequestPreVote(id raft.ServerID, target raft.ServerAddress, args *raft.RequestPreVoteRequest, resp *raft.RequestPreVoteResponse) error {
	return t.InmemTransport.RequestPreVote(id, t.target(id, target), args, resp)
}

func (t *overrideTransport) InstallSnapshot(id raft.ServerID, target raft.ServerAddress, args *raft.InstallSnapshotRequest, resp *raft.InstallSnapshotResponse, data io.Reader) error {
	return t.InmemTransport.InstallSnapshot(id, t.target(id, target), args, resp, data)
}

func (t *overrideTransport) TimeoutNow(id raft.ServerID, target raft.ServerAddress, args *raft.TimeoutNowRequest, resp *raft.TimeoutNowResponse) error {
	return t.InmemTransport.TimeoutNow(id, t.target(id, target), args, resp)
}

// The rebound node holds the shorter log, so its peer refuses to elect it and
// keeps sending to the old address: no leader without help. The rebound node
// asks the peer to route it to the new address, and the peer, once elected,
// commits the change.
func TestReaddressTwoVotersRepairedByPeer(t *testing.T) {
	if testing.Short() {
		t.Skip("requires real in-memory Raft; covered by the native integration lane")
	}
	oldAddress := raft.ServerAddress("192.0.2.11:8180")
	newAddress := raft.ServerAddress("192.0.2.21:8180")
	peerAddress := raft.ServerAddress("192.0.2.12:8180")
	configuration := raft.Configuration{Servers: []raft.Server{
		{ID: "node-1", Address: oldAddress, Suffrage: raft.Voter},
		{ID: "node-2", Address: peerAddress, Suffrage: raft.Voter},
	}}
	raftConfig := func(id raft.ServerID) *raft.Config {
		config := raft.DefaultConfig()
		config.LocalID = id
		config.Logger = hclog.NewNullLogger()
		config.HeartbeatTimeout = 100 * time.Millisecond
		config.ElectionTimeout = 100 * time.Millisecond
		config.LeaderLeaseTimeout = 50 * time.Millisecond
		config.CommitTimeout = 10 * time.Millisecond
		return config
	}
	waitFor := func(description string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !condition() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", description)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	movedDB := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	peerDB := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	movedFSM := clusterModels.NewFSMDispatcher(movedDB)
	clusterModels.RegisterDefaultHandlers(movedFSM)
	peerFSM := clusterModels.NewFSMDispatcher(peerDB)
	clusterModels.RegisterDefaultHandlers(peerFSM)
	movedLogs, movedStable, movedSnapshots := raft.NewInmemStore(), raft.NewInmemStore(), raft.NewInmemSnapshotStore()

	provider := newRaftAddressProvider()
	_, oldTransport := raft.NewInmemTransport(oldAddress)
	_, peerInmem := raft.NewInmemTransport(peerAddress)
	peerTransport := &overrideTransport{InmemTransport: peerInmem, provider: provider}
	oldTransport.Connect(peerAddress, peerInmem)
	peerInmem.Connect(oldAddress, oldTransport)

	original, err := raft.NewRaft(raftConfig("node-1"), movedFSM, movedLogs, movedStable, movedSnapshots, oldTransport)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := raft.NewRaft(raftConfig("node-2"), peerFSM, raft.NewInmemStore(), raft.NewInmemStore(), raft.NewInmemSnapshotStore(), peerTransport)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = peer.Shutdown().Error()
		_ = peerInmem.Close()
	})
	for _, instance := range []*raft.Raft{original, peer} {
		if err := instance.BootstrapCluster(configuration).Error(); err != nil {
			t.Fatal(err)
		}
	}
	waitFor("a leader", func() bool { return original.State() == raft.Leader || peer.State() == raft.Leader })
	if original.State() == raft.Leader {
		if err := original.LeadershipTransferToServer("node-2", peerAddress).Error(); err != nil {
			t.Fatal(err)
		}
	}
	waitFor("node-2 to lead", func() bool { return peer.State() == raft.Leader })
	if err := peer.Barrier(time.Second).Error(); err != nil {
		t.Fatal(err)
	}
	// Both nodes apply a write, so a call that waited would have an index to send.
	writer := withRaft(&Service{DB: peerDB, NodeID: "node-2", fsm: peerFSM}, peer)
	if err := writer.ProposeNoteCreate(t.Context(), "before-the-move", "content"); err != nil {
		t.Fatal(err)
	}
	waitFor("node-1 to apply the write", func() bool { return movedFSM.AppliedIndex() != 0 })

	// node-1 goes down to rebind; node-2 appends an entry it can never commit,
	// leaving node-1 with the shorter log.
	if err := original.Shutdown().Error(); err != nil {
		t.Fatal(err)
	}
	_ = oldTransport.Close()
	peerInmem.Disconnect(oldAddress)
	movedLast, err := movedLogs.LastIndex()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = peer.Barrier(time.Second).Error() }()
	waitFor("node-2 to append past node-1", func() bool { return peer.LastIndex() > movedLast })
	waitFor("node-2 to step down", func() bool { return peer.State() != raft.Leader })

	if err := movedDB.Create(&clusterModels.Cluster{
		Enabled: true, RaftIP: "192.0.2.21", ReaddressOldIP: "192.0.2.11",
		ReaddressNewIP: "192.0.2.21", ReaddressPhase: ReaddressPhaseLocalRebound,
	}).Error; err != nil {
		t.Fatal(err)
	}
	_, movedTransport := raft.NewInmemTransport(newAddress)
	movedTransport.Connect(peerAddress, peerInmem)
	peerInmem.Connect(newAddress, movedTransport)
	moved, err := raft.NewRaft(raftConfig("node-1"), movedFSM, movedLogs, movedStable, movedSnapshots, movedTransport)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = moved.Shutdown().Error()
		_ = movedTransport.Close()
	})

	movedGate := NewMutationGate()
	movedGate.Close()
	movedService := withRaft(&Service{
		DB: movedDB, NodeID: "node-1", mutationGate: movedGate,
		AuthService: &guestIdentityInventoryAuthStub{}, fsm: movedFSM,
	}, moved)
	peerService := withRaft(&Service{
		DB: peerDB, NodeID: "node-2", mutationGate: newOpenTestMutationGate(t),
		AuthService: &guestIdentityInventoryAuthStub{}, addressProvider: provider,
		fsm: peerFSM,
	}, peer)
	nodes := map[string]*Service{"192.0.2.21": movedService, "192.0.2.12": peerService}
	route := func(issuer string) func(context.Context, string, string, []byte, map[string]string) (int, []byte, error) {
		return func(ctx context.Context, method, rawURL string, payload []byte, headers map[string]string) (int, []byte, error) {
			parsed, err := url.Parse(rawURL)
			if err != nil {
				return 0, nil, err
			}
			target := nodes[parsed.Hostname()]
			if target == nil {
				return 0, nil, fmt.Errorf("dial %s: no route to host", parsed.Host)
			}
			name, ok := strings.CutPrefix(parsed.Path, "/api/intra-cluster/node/readdress-")
			if !ok {
				return 0, nil, fmt.Errorf("unexpected request %s %s", method, rawURL)
			}
			if index := headers[CallIndexHeader]; index != "0" {
				t.Errorf("readdress-%s waits for index %s", name, index)
			}
			result, callErr := target.ServeNodeCall(ctx, issuer, headers[CallNodeHeader], 0, "readdress-"+name, payload)
			response := internal.APIResponse[CallResult]{Status: "success", Data: result}
			status := http.StatusOK
			if callErr != nil {
				response = internal.APIResponse[CallResult]{Status: "error", Error: callErr.Error()}
				status = http.StatusConflict
			}
			body, err := json.Marshal(response)
			return status, body, err
		}
	}
	movedService.peerRequest = route("node-1")
	peerService.peerRequest = route("node-2")

	time.Sleep(500 * time.Millisecond)
	if moved.State() == raft.Leader || peer.State() == raft.Leader {
		t.Fatal("a leader was elected without the peer repair; the test no longer covers it")
	}

	waitFor("the readdress to finish", func() bool {
		movedService.reconcileReaddress(context.Background())
		var record clusterModels.Cluster
		return movedDB.First(&record).Error == nil && record.ReaddressPhase == ""
	})
	for _, instance := range []*raft.Raft{moved, peer} {
		waitFor("node-1 at its new address on "+instance.String(), func() bool {
			future := instance.GetConfiguration()
			if future.Error() != nil {
				return false
			}
			server, present, _ := resolveRaftMember(future.Configuration(), "node-1")
			return present && server.Address == newAddress
		})
	}
	if _, err := provider.ServerAddr("node-1"); err == nil {
		t.Fatal("the peer kept its address override after the commit")
	}
	if _, release, err := movedService.EnterMutation(context.Background()); err != nil {
		t.Fatalf("node was not reopened: %v", err)
	} else {
		release()
	}
}

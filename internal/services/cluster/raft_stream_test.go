// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

type countingFSM struct {
	mu      sync.Mutex
	applied int
}

func (f *countingFSM) Apply(*raft.Log) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied++
	return nil
}

func (f *countingFSM) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.applied
}

func (*countingFSM) Snapshot() (raft.FSMSnapshot, error) { return nil, nil }
func (*countingFSM) Restore(io.ReadCloser) error         { return nil }

func startTLSRaft(t *testing.T, s *Service, id string, fsm raft.FSM) (*raft.Raft, raft.ServerAddress) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	transport := raft.NewNetworkTransportWithConfig(&raft.NetworkTransportConfig{
		Stream: s.newRaftStream(listener), MaxPool: 2, Timeout: 2 * time.Second, Logger: hclog.NewNullLogger(),
	})
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(id)
	config.Logger = hclog.NewNullLogger()
	config.HeartbeatTimeout = 200 * time.Millisecond
	config.ElectionTimeout = 200 * time.Millisecond
	config.LeaderLeaseTimeout = 100 * time.Millisecond
	r, err := raft.NewRaft(config, fsm, raft.NewInmemStore(), raft.NewInmemStore(), raft.NewInmemSnapshotStore(), transport)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = r.Shutdown().Error()
		_ = transport.Close()
	})
	return r, transport.LocalAddr()
}

// A listener that accepted the peer waits for the rest of the RPC, so only a refusal
// closes the connection; a plaintext probe needs a full TLS record header to be refused.
func refused(probe []byte, dial func() (net.Conn, error)) bool {
	conn, err := dial()
	if err != nil {
		return true
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, _ = conn.Write(probe)
	_, err = conn.Read(make([]byte, 1))
	timeout, ok := err.(net.Error)
	return !ok || !timeout.Timeout()
}

func TestRaftStreamCarriesRaftOnlyBetweenMembers(t *testing.T) {
	db := newClusterServiceTestDB(t)
	a := newPinnedTestService(t, db, "node-a")
	b := newPinnedTestService(t, db, "node-b")
	a.reloadPins()
	if err := a.ReopenMutations(); err != nil {
		t.Fatal(err)
	}
	fsmA, fsmB := &countingFSM{}, &countingFSM{}
	raftA, addrA := startTLSRaft(t, a, "node-a", fsmA)
	_, addrB := startTLSRaft(t, b, "node-b", fsmB)
	if err := raftA.BootstrapCluster(raft.Configuration{Servers: []raft.Server{
		{ID: "node-a", Address: addrA}, {ID: "node-b", Address: addrB},
	}}).Error(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for raftA.State() != raft.Leader && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if raftA.State() != raft.Leader {
		t.Fatal("node-a didn't become leader over TLS")
	}
	if err := raftA.Apply([]byte("entry"), 5*time.Second).Error(); err != nil {
		t.Fatalf("apply over TLS: %v", err)
	}
	for fsmB.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if fsmB.count() == 0 {
		t.Fatal("node-b didn't receive the entry")
	}

	stranger := withCert(t, NewClusterService(newClusterServiceTestDB(t), nil, nil).(*Service), testClusterCert(t))
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	dialA := func(config *tls.Config) func() (net.Conn, error) {
		return func() (net.Conn, error) { return tls.DialWithDialer(dialer, "tcp", string(addrA), config) }
	}
	if !refused([]byte{0}, dialA(stranger.JoinTLSConfig())) {
		t.Fatal("a stranger's cert was accepted")
	}
	if !refused([]byte{0}, dialA(&tls.Config{InsecureSkipVerify: true})) {
		t.Fatal("a client without a cert was accepted")
	}
	if !refused(make([]byte, 16), func() (net.Conn, error) { return dialer.Dial("tcp", string(addrA)) }) {
		t.Fatal("plaintext was accepted")
	}
	if refused([]byte{0}, dialA(b.PeerTLSConfig())) {
		t.Fatal("a member was turned away")
	}

	if err := a.apply("cluster_member", "remove", clusterModels.ClusterMemberRemoval{NodeID: "node-b"}); err != nil {
		t.Fatal(err)
	}
	if !refused([]byte{0}, dialA(b.PeerTLSConfig())) {
		t.Fatal("a removed member still gets in")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stream := a.newRaftStream(listener)
	defer stream.Close()
	if conn, err := stream.Dial(addrB, 2*time.Second); err == nil {
		conn.Close()
		t.Fatal("a member's Raft still dials a removed one")
	}
}

func TestJoinAdmissionSeedsTheMembersPins(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	joiner := NewClusterService(newClusterServiceTestDB(t, guestIdentityJoinTestModels()...), nil, nil).(*Service)
	leaderPin := CertPin(testClusterCert(t).Leaf)
	joiner.peerRequest = func(_ context.Context, _, rawURL string, payload []byte, _ map[string]string) (int, []byte, error) {
		var request JoinAdmissionRequest
		if !strings.HasSuffix(rawURL, "/api/cluster/accept-join") || json.Unmarshal(payload, &request) != nil ||
			request.SSHPublicKey == "" {
			return http.StatusBadRequest, []byte(`{"status":"error"}`), nil
		}
		body, err := json.Marshal(internal.APIResponse[ClusterJoinStatus]{Status: "success", Data: ClusterJoinStatus{
			Phase: JoinPhaseCatchingUp, Pins: map[string]string{"leader": hex.EncodeToString(leaderPin[:])},
		}})
		return http.StatusAccepted, body, err
	}
	if err := joiner.requestAdmission(t.Context(), clusterModels.Cluster{
		RaftIP: "127.0.0.2", JoinLeaderIP: "127.0.0.1", Key: guestIdentityJoinTestKey,
	}); err != nil {
		t.Fatal(err)
	}
	if node, ok := joiner.PinNode(leaderPin); !ok || node != "leader" {
		t.Fatalf("the leader's pin maps to %q (%t)", node, ok)
	}
}

func TestJoinerTrustsSeededPinsUntilItCatchesUp(t *testing.T) {
	db := newClusterServiceTestDB(t)
	joiner := NewClusterService(db, nil, nil).(*Service)
	joiner.NodeID = "joiner"
	if err := joiner.ReopenMutations(); err != nil {
		t.Fatal(err)
	}
	leaderPin, ghostPin := CertPin(testClusterCert(t).Leaf), CertPin(testClusterCert(t).Leaf)
	joiner.seedJoinPins(map[string]string{
		"leader": hex.EncodeToString(leaderPin[:]), "ghost": hex.EncodeToString(ghostPin[:]),
	})
	if err := joiner.clearClusteredData(); err != nil {
		t.Fatal(err)
	}
	for _, pin := range [][32]byte{leaderPin, ghostPin} {
		if _, ok := joiner.PinNode(pin); !ok {
			t.Fatal("a seeded pin isn't trusted while the joiner catches up")
		}
	}

	publish := func(nodeID string, pin [32]byte) {
		t.Helper()
		if err := joiner.UpsertClusterSSHIdentity(t.Context(), clusterModels.ClusterSSHIdentity{
			NodeUUID: nodeID, SSHHost: "10.0.0.1", PublicKey: "ssh-ed25519 AAAA", CertPin: hex.EncodeToString(pin[:]),
		}); err != nil {
			t.Fatal(err)
		}
	}
	publish("leader", leaderPin)
	if _, ok := joiner.PinNode(ghostPin); !ok {
		t.Fatal("the seed was dropped before the joiner's own identity arrived")
	}
	publish("joiner", CertPin(testClusterCert(t).Leaf))
	if _, ok := joiner.PinNode(ghostPin); ok {
		t.Fatal("a pin only the seed had is still trusted after catching up")
	}
	if node, ok := joiner.PinNode(leaderPin); !ok || node != "leader" {
		t.Fatalf("the leader's replicated pin maps to %q (%t)", node, ok)
	}
}

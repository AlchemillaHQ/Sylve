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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster/raftdir"
	"github.com/hashicorp/raft"
)

type rebuildTestNode struct {
	id       string
	service  *Service
	rebuild  *rebuild
	server   *httptest.Server
	founded  int
	rejoined string
	done     bool
	lastErr  error
}

var rebuildTestMembers = []raft.Server{
	{ID: "node-a", Address: "192.0.2.1:8180", Suffrage: raft.Voter},
	{ID: "node-b", Address: "192.0.2.2:8180", Suffrage: raft.Voter},
	{ID: "node-c", Address: "192.0.2.3:8180", Suffrage: raft.Voter},
}

func newRebuildTestNode(t *testing.T, id string, term, index uint64, key string) *rebuildTestNode {
	t.Helper()
	service := newStandaloneTestService(t)
	service.NodeID = id
	if err := service.SetClusterCert(testClusterCert(t)); err != nil {
		t.Fatal(err)
	}
	node := &rebuildTestNode{id: id, service: service}
	node.rebuild = &rebuild{dir: t.TempDir(), state: raftdir.Rebuild{
		Key: key, Members: rebuildTestMembers, LastTerm: term, LastIndex: index,
	}}
	node.rebuild.found = func(context.Context) error {
		node.founded++
		return node.rebuild.setPhase(raftdir.RebuildFounded)
	}
	node.rebuild.rejoin = func(_ context.Context, founder raft.Server) error {
		node.rejoined = string(founder.ID)
		return nil
	}
	service.rebuild.Store(node.rebuild)
	node.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message RebuildMessage
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		exporter, err := r.TLS.ExportKeyingMaterial(RebuildExporterLabel, nil, 32)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reply, proof, err := service.AnswerRebuild(r.Context(), exporter, r.Header.Get(RebuildProofHeader), message)
		if proof != "" {
			w.Header().Set(RebuildProofHeader, proof)
		}
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(internal.APIResponse[any]{Status: "error", Error: err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(internal.APIResponse[RebuildReply]{Status: "success", Data: reply})
	}))
	node.server.TLS = service.ServerTLSConfig()
	t.Cleanup(node.server.Close)
	return node
}

func (n *rebuildTestNode) upgrade() *rebuildTestNode {
	n.server.StartTLS()
	return n
}

func routeRebuildTestNodes(t *testing.T, nodes ...*rebuildTestNode) {
	t.Helper()
	original := rebuildPeerHost
	rebuildPeerHost = func(server raft.Server) string {
		for _, node := range nodes {
			if node.id == string(server.ID) && node.server.URL != "" {
				return strings.TrimPrefix(node.server.URL, "https://")
			}
		}
		return "127.0.0.1:1"
	}
	t.Cleanup(func() { rebuildPeerHost = original })
}

func stepRebuild(t *testing.T, rounds int, nodes ...*rebuildTestNode) {
	t.Helper()
	for range rounds {
		for _, node := range nodes {
			if node.done {
				continue
			}
			node.done, node.lastErr = node.service.rebuildStep(t.Context(), node.rebuild)
		}
	}
}

func TestRebuildFoundsTheMostUpToDateMember(t *testing.T) {
	a := newRebuildTestNode(t, "node-a", 3, 10, "old-key").upgrade()
	b := newRebuildTestNode(t, "node-b", 3, 12, "old-key").upgrade()
	c := newRebuildTestNode(t, "node-c", 2, 20, "old-key").upgrade()
	routeRebuildTestNodes(t, a, b, c)
	reconciled := make(chan struct{}, 1)
	b.service.SetJoinCompleteHook(func() { reconciled <- struct{}{} })

	stepRebuild(t, 3, a, b, c)
	select {
	case <-reconciled:
	case <-time.After(3 * time.Second):
		t.Fatal("the founder didn't reconcile as a member")
	}
	if b.founded != 1 || a.founded != 0 || c.founded != 0 {
		t.Fatalf("founded: a=%d b=%d c=%d", a.founded, b.founded, c.founded)
	}
	if a.rejoined != "node-b" || c.rejoined != "node-b" || !a.done || !c.done {
		t.Fatalf("a rejoined %q (done %t), c rejoined %q (done %t)", a.rejoined, a.done, c.rejoined, c.done)
	}
}

func TestRebuildWaitsForAMajorityOfTheOldVoters(t *testing.T) {
	a := newRebuildTestNode(t, "node-a", 3, 12, "old-key").upgrade()
	b := newRebuildTestNode(t, "node-b", 3, 10, "old-key")
	c := newRebuildTestNode(t, "node-c", 3, 30, "old-key")
	routeRebuildTestNodes(t, a, b, c)

	stepRebuild(t, 3, a)
	if a.founded != 0 || a.lastErr == nil || !strings.Contains(a.lastErr.Error(), "rebuild_waiting_for_majority") {
		t.Fatalf("alone: founded %d, %v", a.founded, a.lastErr)
	}
	b.upgrade()
	stepRebuild(t, 3, a, b)
	if a.founded != 1 || b.rejoined != "node-a" {
		t.Fatalf("with b: a founded %d, b rejoined %q", a.founded, b.rejoined)
	}
}

func TestRebuildKeepsAVote(t *testing.T) {
	a := newRebuildTestNode(t, "node-a", 1, 5, "old-key").upgrade()
	c := newRebuildTestNode(t, "node-c", 1, 9, "old-key").upgrade()
	routeRebuildTestNodes(t, a, c)
	if vote, err := a.rebuild.vote("node-b"); err != nil || vote != "node-b" {
		t.Fatalf("vote = %q, %v", vote, err)
	}

	stepRebuild(t, 3, a, c)
	if c.founded != 0 || c.lastErr == nil || !strings.Contains(c.lastErr.Error(), "rebuild_waiting_for_votes") {
		t.Fatalf("c: founded %d, %v", c.founded, c.lastErr)
	}
	if _, saved, err := raftdir.FindRebuild(a.rebuild.dir); err != nil || saved.Vote != "node-b" {
		t.Fatalf("a's saved vote = %+v, %v", saved, err)
	}
}

func TestRebuildCountsOnlyHoldersOfTheOldKey(t *testing.T) {
	a := newRebuildTestNode(t, "node-a", 3, 12, "old-key").upgrade()
	b := newRebuildTestNode(t, "node-b", 3, 10, "another-key").upgrade()
	routeRebuildTestNodes(t, a, b)

	stepRebuild(t, 3, a, b)
	if a.founded != 0 || b.founded != 0 || !strings.Contains(a.lastErr.Error(), "rebuild_waiting_for_majority") {
		t.Fatalf("founded a=%d b=%d; a: %v", a.founded, b.founded, a.lastErr)
	}
	if _, _, err := b.service.AnswerRebuild(t.Context(), []byte("session"), rebuildProof("old-key", "request", []byte("session")),
		RebuildMessage{NodeID: "node-a"}); !errors.Is(err, ErrRebuildProofInvalid) {
		t.Fatalf("b answered a proof with the wrong key: %v", err)
	}
}

func TestRebuildForcedFoundsAlone(t *testing.T) {
	a := newRebuildTestNode(t, "node-a", 1, 1, "old-key")
	a.rebuild.state.Force = true
	routeRebuildTestNodes(t, a)
	stepRebuild(t, 1, a)
	if a.founded != 1 {
		t.Fatalf("forced rebuild founded %d times: %v", a.founded, a.lastErr)
	}
}

func TestRebuildFounderHandsOutOneJoinKeyAtATime(t *testing.T) {
	founder := newRebuildTestNode(t, "node-a", 1, 1, "old-key")
	if err := founder.service.DB.Create(&clusterModels.Cluster{Enabled: true, RaftIP: "192.0.2.1"}).Error; err != nil {
		t.Fatal(err)
	}
	startSoleVoter(t, founder.service, "node-a")
	if err := founder.rebuild.setPhase(raftdir.RebuildFounded); err != nil {
		t.Fatal(err)
	}
	ask := func(nodeID, key string) (RebuildReply, error) {
		session := []byte("session of " + nodeID)
		reply, _, err := founder.service.AnswerRebuild(t.Context(), session, rebuildProof(key, "request", session),
			RebuildMessage{NodeID: nodeID, JoinKey: true})
		return reply, err
	}

	first, err := ask("node-b", "old-key")
	if err != nil || first.JoinKey == "" || !first.Founded {
		t.Fatalf("first: %+v, %v", first, err)
	}
	if reply, err := ask("node-c", "old-key"); err != nil || reply.JoinKey != "" {
		t.Fatalf("while node-b holds a key: %+v, %v", reply, err)
	}
	if reply, err := ask("node-b", "old-key"); err != nil || reply.JoinKey == "" || reply.JoinKey == first.JoinKey {
		t.Fatalf("node-b again: %+v, %v", reply, err)
	}
	founder.rebuild.keyIssued = time.Now().Add(-2 * rebuildKeyHold)
	if reply, err := ask("node-c", "old-key"); err != nil || reply.JoinKey == "" {
		t.Fatalf("after node-b's hold: %+v, %v", reply, err)
	}
	if _, err := ask("node-x", "old-key"); !errors.Is(err, ErrRebuildNodeUnknown) {
		t.Fatalf("a stranger: %v", err)
	}
	if _, err := ask("node-c", "guessed-key"); !errors.Is(err, ErrRebuildProofInvalid) {
		t.Fatalf("without the key: %v", err)
	}
	if record, err := founder.service.loadClusterRecord(); err != nil || !record.Enabled {
		t.Fatalf("record = %+v, %v", record, err)
	}
}

func TestRebuildVotesOnlyForAMemberAtLeastAsUpToDate(t *testing.T) {
	a := newRebuildTestNode(t, "node-a", 3, 12, "old-key")
	ask := func(candidate string, term, index uint64) string {
		session := []byte("session of " + candidate)
		reply, _, err := a.service.AnswerRebuild(t.Context(), session, rebuildProof("old-key", "request", session),
			RebuildMessage{NodeID: candidate, LastTerm: term, LastIndex: index, Candidate: true})
		if err != nil {
			t.Fatal(err)
		}
		return reply.Vote
	}
	if vote := ask("node-b", 3, 10); vote != "" {
		t.Fatalf("voted for a candidate behind it: %q", vote)
	}
	if vote := ask("node-c", 3, 13); vote != "node-c" {
		t.Fatalf("didn't vote for one ahead: %q", vote)
	}
}

func TestRebuildVoterDoesNotStand(t *testing.T) {
	a := newRebuildTestNode(t, "node-a", 1, 9, "old-key").upgrade()
	c := newRebuildTestNode(t, "node-c", 1, 5, "old-key").upgrade()
	routeRebuildTestNodes(t, a, c)
	if _, err := a.rebuild.vote("node-b"); err != nil {
		t.Fatal(err)
	}
	stepRebuild(t, 3, a, c)
	if a.founded != 0 || c.founded != 0 || a.rebuild.snapshot().Vote != "node-b" {
		t.Fatalf("founded a=%d c=%d; a's vote %q", a.founded, c.founded, a.rebuild.snapshot().Vote)
	}
}

func TestRebuildNonvoterDoesNotStand(t *testing.T) {
	members := []raft.Server{
		{ID: "node-a", Address: "192.0.2.1:8180", Suffrage: raft.Voter},
		{ID: "node-b", Address: "192.0.2.2:8180", Suffrage: raft.Voter},
		{ID: "node-d", Address: "192.0.2.4:8180", Suffrage: raft.Nonvoter},
	}
	a := newRebuildTestNode(t, "node-a", 1, 5, "old-key").upgrade()
	d := newRebuildTestNode(t, "node-d", 1, 99, "old-key").upgrade()
	a.rebuild.state.Members, d.rebuild.state.Members = members, members
	routeRebuildTestNodes(t, a, d)
	stepRebuild(t, 3, d)
	if d.founded != 0 || d.rebuild.snapshot().Vote != "" || !strings.Contains(d.lastErr.Error(), "rebuild_waiting_for_founder") {
		t.Fatalf("nonvoter: founded %d, vote %q, %v", d.founded, d.rebuild.snapshot().Vote, d.lastErr)
	}
}

func TestRebuildIgnoresAnAnswerWithoutTheKey(t *testing.T) {
	a := newRebuildTestNode(t, "node-a", 1, 5, "old-key")
	impostor := newRebuildTestNode(t, "node-b", 1, 9, "old-key")
	impostor.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(RebuildProofHeader, strings.Repeat("0", 64))
		_ = json.NewEncoder(w).Encode(internal.APIResponse[RebuildReply]{
			Status: "success", Data: RebuildReply{NodeID: "node-b", Founded: true, JoinKey: "x.y"},
		})
	})
	impostor.upgrade()
	routeRebuildTestNodes(t, a, impostor)
	stepRebuild(t, 3, a)
	if a.rejoined != "" || a.done {
		t.Fatalf("a followed the impostor: rejoined %q", a.rejoined)
	}
}

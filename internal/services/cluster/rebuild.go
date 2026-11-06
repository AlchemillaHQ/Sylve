// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/cluster/raftdir"
	"github.com/hashicorp/raft"
)

// A rebuild message carries a MAC, with the old cluster key, of its TLS
// session's exporter, so the key never crosses the network and a message can't
// be relayed into another session.

const (
	RebuildProofHeader   = "X-Sylve-Rebuild-Proof"
	RebuildExporterLabel = "EXPORTER-sylve-rebuild"
	rebuildInterval      = 3 * time.Second
	rebuildTimeout       = 5 * time.Second
	// How long the founder waits for a node it handed a join key to before
	// handing one to another: a new key replaces the last.
	rebuildKeyHold = time.Minute
)

var (
	ErrRebuildNotRunning   = errors.New("rebuild_not_running")
	ErrRebuildProofInvalid = errors.New("rebuild_proof_invalid")
	ErrRebuildNodeUnknown  = errors.New("rebuild_node_unknown")
)

type RebuildMessage struct {
	NodeID    string `json:"nodeId"`
	LastTerm  uint64 `json:"lastTerm"`
	LastIndex uint64 `json:"lastIndex"`
	Candidate bool   `json:"candidate,omitempty"`
	JoinKey   bool   `json:"joinKey,omitempty"`
}

type RebuildReply struct {
	NodeID    string `json:"nodeId"`
	LastTerm  uint64 `json:"lastTerm"`
	LastIndex uint64 `json:"lastIndex"`
	Vote      string `json:"vote,omitempty"`
	Founded   bool   `json:"founded,omitempty"`
	JoinKey   string `json:"joinKey,omitempty"`
}

type rebuild struct {
	mu        sync.Mutex
	dir       string
	state     raftdir.Rebuild
	keyHolder string
	keyIssued time.Time
	lastWait  string
	found     func(context.Context) error
	rejoin    func(context.Context, raft.Server) error
}

var rebuildPeerHost = func(server raft.Server) string {
	return ClusterAPIHost(raftAddressHost(string(server.Address)))
}

func rebuildAhead(aTerm, aIndex uint64, aID string, bTerm, bIndex uint64, bID string) bool {
	if aTerm != bTerm {
		return aTerm > bTerm
	}
	if aIndex != bIndex {
		return aIndex > bIndex
	}
	return aID > bID
}

func rebuildProof(key, side string, exporter []byte) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(side))
	mac.Write(exporter)
	return hex.EncodeToString(mac.Sum(nil))
}

func rebuildVoters(members []raft.Server) map[string]raft.Server {
	voters := make(map[string]raft.Server)
	for _, member := range members {
		if member.Suffrage == raft.Voter {
			voters[string(member.ID)] = member
		}
	}
	return voters
}

func (r *rebuild) save() error {
	return r.state.Save(r.dir)
}

func (r *rebuild) snapshot() raftdir.Rebuild {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// One vote per voter, saved before it's given: across a restart, two
// candidates could otherwise each win a majority.
func (r *rebuild) vote(candidate string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.Vote == "" {
		r.state.Vote = candidate
		if err := r.save(); err != nil {
			r.state.Vote = ""
			return "", err
		}
	}
	return r.state.Vote, nil
}

func (r *rebuild) setPhase(phase string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Phase = phase
	return r.save()
}

func (r *rebuild) moveAside(raftDir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dir != raftDir {
		return nil
	}
	preDir := raftdir.PreDir(raftDir)
	if err := os.Rename(raftDir, preDir); err != nil {
		return fmt.Errorf("rebuild_move_old_raft_state_failed: %w", err)
	}
	r.dir = preDir
	return os.MkdirAll(raftDir, 0o700)
}

func (r *rebuild) finish() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return raftdir.RemoveRebuild(r.dir)
}

func (s *Service) resumeRebuild(raftDir string) (bool, error) {
	dir, state, err := raftdir.FindRebuild(raftDir)
	if err != nil || state == nil {
		return false, err
	}
	s.startRebuild(&rebuild{dir: dir, state: *state})
	return state.Phase != raftdir.RebuildFounded, nil
}

func (s *Service) startRebuild(r *rebuild) {
	r.found = s.foundRebuilt
	r.rejoin = s.rejoinRebuilt
	s.rebuild.Store(r)
	logger.L.Warn().Str("old_raft_state", r.dir).
		Msg("Raft state is from an older build: rebuilding the cluster with the other upgraded members")
	go s.runRebuild(context.Background(), r)
}

func (s *Service) runRebuild(ctx context.Context, r *rebuild) {
	for {
		done, err := s.rebuildStep(ctx, r)
		if done {
			s.rebuild.CompareAndSwap(r, nil)
			logger.L.Info().Msg("cluster_rebuild_finished")
			return
		}
		if wait := fmt.Sprint(err); err != nil && wait != r.lastWait {
			r.lastWait = wait
			logger.L.Warn().Err(err).Msg("cluster_rebuild_waiting")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(rebuildInterval):
		}
	}
}

func (s *Service) rebuildStep(ctx context.Context, r *rebuild) (bool, error) {
	self := s.LocalNodeID()
	state := r.snapshot()
	switch {
	case state.Phase == raftdir.RebuildFounded:
		return s.rebuildRejoined(r)
	case state.Phase == raftdir.RebuildRejoining && s.Raft() != nil:
		return true, r.finish()
	case state.Force || state.Phase == raftdir.RebuildFounding:
		return false, s.foundRebuild(ctx, r)
	}

	message := RebuildMessage{NodeID: self, LastTerm: state.LastTerm, LastIndex: state.LastIndex}
	replies := make(map[string]RebuildReply)
	for _, member := range state.Members {
		if string(member.ID) == self {
			continue
		}
		reply, err := s.askRebuild(ctx, member, state.Key, message)
		if err != nil {
			continue
		}
		if reply.Founded {
			if err := r.rejoin(ctx, member); err != nil {
				return false, err
			}
			return true, r.finish()
		}
		replies[string(member.ID)] = reply
	}
	if state.Phase == raftdir.RebuildRejoining {
		return false, fmt.Errorf("rebuild_waiting_for_founder")
	}

	voters := rebuildVoters(state.Members)
	majority := len(voters)/2 + 1
	if _, ok := voters[self]; !ok {
		return false, fmt.Errorf("rebuild_waiting_for_founder")
	}
	answered := 1
	for id, reply := range replies {
		if _, ok := voters[id]; !ok {
			continue
		}
		answered++
		if rebuildAhead(reply.LastTerm, reply.LastIndex, id, state.LastTerm, state.LastIndex, self) {
			return false, fmt.Errorf("rebuild_waiting_for_founder: %s is ahead", id)
		}
	}
	if answered < majority {
		return false, fmt.Errorf("rebuild_waiting_for_majority: %d of %d old voters upgraded", answered, len(voters))
	}
	if vote, err := r.vote(self); err != nil || vote != self {
		return false, errors.Join(err, fmt.Errorf("rebuild_waiting_for_founder: voted for %s", vote))
	}
	votes := 1
	message.Candidate = true
	for id := range replies {
		if voter, ok := voters[id]; ok {
			if reply, err := s.askRebuild(ctx, voter, state.Key, message); err == nil && reply.Vote == self {
				votes++
			}
		}
	}
	if votes < majority {
		return false, fmt.Errorf("rebuild_waiting_for_votes: %d of %d", votes, majority)
	}
	return false, s.foundRebuild(ctx, r)
}

// The founder then reconciles what a joiner does once it's a member: at
// startup it had no Raft to do it with.
func (s *Service) foundRebuild(ctx context.Context, r *rebuild) error {
	if err := r.found(ctx); err != nil {
		return err
	}
	s.notifyJoinComplete()
	return nil
}

func (s *Service) rebuildRejoined(r *rebuild) (bool, error) {
	for _, member := range r.snapshot().Members {
		if !s.raftHasMember(string(member.ID)) {
			return false, fmt.Errorf("rebuild_waiting_for_rejoin: %s", member.ID)
		}
	}
	return true, r.finish()
}

func (s *Service) raftHasMember(nodeID string) bool {
	r := s.Raft()
	if r == nil {
		return false
	}
	future := r.GetConfiguration()
	if future.Error() != nil {
		return false
	}
	for _, server := range future.Configuration().Servers {
		if string(server.ID) == nodeID {
			return true
		}
	}
	return false
}

// It dials and shakes hands first, so the MAC covers the session the message goes in.
func (s *Service) askRebuild(ctx context.Context, member raft.Server, key string, message RebuildMessage) (RebuildReply, error) {
	var reply RebuildReply
	payload, err := json.Marshal(message)
	if err != nil {
		return reply, err
	}
	ctx, cancel := context.WithTimeout(ctx, rebuildTimeout)
	defer cancel()
	host := rebuildPeerHost(member)
	dialer := tls.Dialer{Config: s.JoinTLSConfig()}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return reply, err
	}
	defer conn.Close()
	tlsState := conn.(*tls.Conn).ConnectionState()
	exporter, err := tlsState.ExportKeyingMaterial(RebuildExporterLabel, nil, 32)
	if err != nil {
		return reply, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+host+"/api/cluster/rebuild", bytes.NewReader(payload))
	if err != nil {
		return reply, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(RebuildProofHeader, rebuildProof(key, "request", exporter))
	transport := &http.Transport{
		DialTLSContext:    func(context.Context, string, string) (net.Conn, error) { return conn, nil },
		DisableKeepAlives: true,
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		return reply, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return reply, err
	}
	if !hmac.Equal([]byte(response.Header.Get(RebuildProofHeader)), []byte(rebuildProof(key, "reply", exporter))) {
		return reply, fmt.Errorf("%w: %s answered without the old cluster key", ErrRebuildProofInvalid, member.ID)
	}
	var answer internal.APIResponse[RebuildReply]
	if err := json.Unmarshal(body, &answer); err != nil {
		return reply, err
	}
	if response.StatusCode != http.StatusOK {
		return reply, fmt.Errorf("rebuild_peer_refused: %s: %s", member.ID, answer.Error)
	}
	return answer.Data, nil
}

func (s *Service) AnswerRebuild(ctx context.Context, exporter []byte, proof string, message RebuildMessage) (RebuildReply, string, error) {
	r := s.rebuild.Load()
	if r == nil {
		return RebuildReply{}, "", ErrRebuildNotRunning
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !hmac.Equal([]byte(proof), []byte(rebuildProof(r.state.Key, "request", exporter))) {
		return RebuildReply{}, "", ErrRebuildProofInvalid
	}
	replyProof := rebuildProof(r.state.Key, "reply", exporter)
	member := false
	for _, server := range r.state.Members {
		member = member || string(server.ID) == message.NodeID
	}
	if !member {
		return RebuildReply{}, replyProof, ErrRebuildNodeUnknown
	}

	self := s.LocalNodeID()
	founded := r.state.Phase == raftdir.RebuildFounded
	_, voter := rebuildVoters(r.state.Members)[message.NodeID]
	if message.Candidate && voter && !founded && r.state.Vote == "" &&
		!rebuildAhead(r.state.LastTerm, r.state.LastIndex, self, message.LastTerm, message.LastIndex, message.NodeID) {
		r.state.Vote = message.NodeID
		if err := r.save(); err != nil {
			r.state.Vote = ""
			return RebuildReply{}, replyProof, err
		}
	}
	reply := RebuildReply{
		NodeID: self, LastTerm: r.state.LastTerm, LastIndex: r.state.LastIndex,
		Vote: r.state.Vote, Founded: founded,
	}
	if message.JoinKey && founded && (r.keyHolder == "" || r.keyHolder == message.NodeID ||
		time.Since(r.keyIssued) > rebuildKeyHold || s.raftHasMember(r.keyHolder)) {
		key, _, err := s.GenerateJoinKey(ctx)
		if err != nil {
			return RebuildReply{}, replyProof, err
		}
		reply.JoinKey = key.String()
		r.keyHolder, r.keyIssued = message.NodeID, time.Now()
	}
	return reply, replyProof, nil
}

func (s *Service) foundRebuilt(ctx context.Context) error {
	r := s.rebuild.Load()
	raftDir, err := config.GetRaftPath()
	if err != nil {
		return err
	}
	if err := r.setPhase(raftdir.RebuildFounding); err != nil {
		return err
	}
	if err := r.moveAside(raftDir); err != nil {
		return err
	}
	// A retry after a later step failed goes on with the Raft it started.
	if s.Raft() == nil {
		if err := s.CleanRaftDir(); err != nil {
			return err
		}
		if _, err := s.setupRaft(true); err != nil {
			return err
		}
	}
	if _, _, err := s.waitUntilLeader(raftLeaderWaitTimeout); err != nil {
		return err
	}
	if err := s.snapshotPreClusterState(); err != nil {
		return err
	}
	if _, err := s.reconcileOwnGuestIdentityClaims(ctx); err != nil {
		return fmt.Errorf("founding_guest_identity_claims_failed: %w", err)
	}
	record, err := s.loadClusterRecord()
	if err != nil {
		return err
	}
	// A rejoining node that founds after all was left unclustered.
	bootstrap := true
	if err := s.DB.Model(&clusterModels.Cluster{}).Where("id = ?", record.ID).Updates(map[string]any{
		"enabled": true, "key": "", "join_leader_ip": "", "raft_bootstrap": &bootstrap,
	}).Error; err != nil {
		return err
	}
	if err := r.setPhase(raftdir.RebuildFounded); err != nil {
		return err
	}
	if err := s.EnsureAndPublishLocalSSHIdentity(ctx); err != nil {
		logger.L.Warn().Err(err).Msg("Cluster SSH identity publish deferred after the rebuild")
	}
	if err := s.PopulateClusterNodes(); err != nil {
		logger.L.Warn().Err(err).Msg("cluster_node_population_deferred_after_rebuild")
	}
	logger.L.Warn().Msg("Founded the rebuilt cluster from this node's database; the other members rejoin it")
	return nil
}

func (s *Service) rejoinRebuilt(ctx context.Context, founder raft.Server) error {
	r := s.rebuild.Load()
	record, err := s.loadClusterRecord()
	if err != nil {
		return err
	}
	if r.snapshot().Phase == raftdir.RebuildRejoining {
		switch {
		case record.Enabled:
			return nil
		case strings.TrimSpace(record.JoinLeaderIP) != "":
			return fmt.Errorf("rebuild_waiting_for_join")
		}
	}
	state := r.snapshot()
	reply, err := s.askRebuild(ctx, founder, state.Key, RebuildMessage{
		NodeID: s.LocalNodeID(), LastTerm: state.LastTerm, LastIndex: state.LastIndex, JoinKey: true,
	})
	if err != nil {
		return err
	}
	if reply.JoinKey == "" {
		return fmt.Errorf("rebuild_waiting_for_join_key: %s admits another node", founder.ID)
	}
	raftDir, err := config.GetRaftPath()
	if err != nil {
		return err
	}
	if err := r.setPhase(raftdir.RebuildRejoining); err != nil {
		return err
	}
	if err := r.moveAside(raftDir); err != nil {
		return err
	}
	bootstrap := false
	if err := s.DB.Model(&clusterModels.Cluster{}).Where("id = ?", record.ID).Updates(map[string]any{
		"enabled": false, "key": "", "raft_bootstrap": &bootstrap,
	}).Error; err != nil {
		return err
	}
	if _, err := s.Join(ctx, raftAddressHost(string(founder.Address)), record.RaftIP, reply.JoinKey); err != nil {
		return err
	}
	record, err = s.loadClusterRecord()
	if err != nil {
		return err
	}
	if !record.Enabled {
		return fmt.Errorf("rebuild_waiting_for_join")
	}
	return nil
}

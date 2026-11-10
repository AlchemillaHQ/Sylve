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
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

const proposalForwardTimeout = 30 * time.Second

type ProposalResult struct {
	Index uint64 `json:"index"`
}

var errConsensusUnavailable = fmt.Errorf("cluster_consensus_unavailable: %w", raft.ErrRaftShutdown)

var ErrClusterProtocolOutdated = errors.New("cluster_protocol_outdated")

// proposable also lists the only commands a peer may hand the leader. A command
// that speaks for a node returns that node, and only that node may propose it.
var proposable = map[string]func(data json.RawMessage) string{
	"note/create":                 nil,
	"note/update":                 nil,
	"note/delete":                 nil,
	"note/bulk_delete":            nil,
	"encryption_key/upsert":       nil,
	"encryption_key/delete":       nil,
	"cluster_join_key/set":        nil,
	"cluster_identity/initialize": nil,
	"cluster_ssh_identity/upsert": func(data json.RawMessage) string {
		var identity clusterModels.ClusterSSHIdentity
		if json.Unmarshal(data, &identity) != nil {
			return ""
		}
		return strings.TrimSpace(identity.NodeUUID)
	},
	"guest_identity/claim":   guestIdentityClaimOwner,
	"guest_identity/release": guestIdentityClaimOwner,
	"acl_grant/set":          nil,
	"acl_grant/delete":       nil,
	"acl_role/set":           nil,
	"acl_role/delete":        nil,
	"acl_group/set":          nil,
	"acl_group/delete":       nil,
	"acl_subject/delete":     nil,
	"acl_subject/rename":     nil,
	"sylve_user/create":      nil,
	"sylve_user/update":      nil,
	"sylve_user/rename":      nil,
	"sylve_user/delete":      nil,
	"auth_realm/set":         nil,
	"auth_realm/delete":      nil,
	"oidc_user/create":       nil,
	"oidc_user/login":        nil,
	"oidc_user/update":       nil,
	"oidc_user/delete":       nil,
}

func guestIdentityClaimOwner(data json.RawMessage) string {
	var set clusterModels.GuestIdentityClaimSet
	if json.Unmarshal(data, &set) != nil {
		return ""
	}
	return strings.TrimSpace(set.OwnerNodeID)
}

// The codes cross the wire between nodes. A readdress fence is also a leave
// fence, so it comes first.
var proposalErrors = []struct {
	code string
	err  error
}{
	{"not_found", gorm.ErrRecordNotFound},
	{"not_leader", raft.ErrNotLeader},
	{"cluster_leadership_changed", raft.ErrLeadershipLost},
	{"cluster_consensus_unavailable", raft.ErrRaftShutdown},
	{"cluster_apply_timeout", raft.ErrEnqueueTimeout},
	{"cluster_protocol_outdated", ErrClusterProtocolOutdated},
	{"node_readdress_fenced", ErrNodeReaddressFenced},
	{"node_leave_fenced", ErrNodeLeaveFenced},
	{"guest_id_already_in_use", clusterModels.ErrGuestIdentityAlreadyInUse},
	{"guest_identity_claim_conflict", clusterModels.ErrGuestIdentityClaimConflict},
	{"peer_removal_blocked", ErrPeerRemovalBlocked},
	{"peer_not_found", ErrPeerNotFound},
}

func ProposalErrorCode(err error) string {
	for _, known := range proposalErrors {
		if errors.Is(err, known.err) {
			return known.code
		}
	}
	return "raft_proposal_failed"
}

func proposalError(code, text string) error {
	for _, known := range proposalErrors {
		if known.code == code {
			return fmt.Errorf("%w: %s", known.err, text)
		}
	}
	return errors.New(text)
}

func (s *Service) Propose(ctx context.Context, commandType, action string, payload any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := proposable[commandType+"/"+action]; !ok {
		return fmt.Errorf("raft_command_not_proposable: %s/%s", commandType, action)
	}
	cmd, err := newCommand(commandType, action, payload)
	if err != nil {
		return err
	}
	if r := s.Raft(); r != nil && r.State() != raft.Leader {
		// An older leader cannot classify commands it does not know.
		if err := s.requireMemberProtocol(r, s.fsm.Protocol(cmd.Type, cmd.Action)); err != nil {
			return err
		}
		index, err := s.forwardProposal(ctx, r, cmd)
		if err != nil {
			return err
		}
		s.WaitForLocalApply(ctx, index)
		return nil
	}
	return s.applyCommand(ctx, cmd)
}

// apply never forwards (a follower gets not_leader): writes that validate
// leader state first use it, so that the whole operation runs on the leader.
func (s *Service) apply(commandType, action string, payload any) error {
	cmd, err := newCommand(commandType, action, payload)
	if err != nil {
		return err
	}
	return s.applyCommand(context.Background(), cmd)
}

func newCommand(commandType, action string, payload any) (clusterModels.Command, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return clusterModels.Command{}, fmt.Errorf("marshal_%s_%s_payload: %w", commandType, action, err)
	}
	return clusterModels.Command{Type: commandType, Action: action, Data: data}, nil
}

func (s *Service) applyCommand(ctx context.Context, cmd clusterModels.Command) error {
	standalone, err := s.RuntimeStateBypassRaft()
	if err != nil {
		return err
	}
	if !standalone {
		_, err := s.applyProposal(ctx, cmd)
		return err
	}
	if err := clusterModels.PrepareCommand(&cmd, time.Now()); err != nil {
		return err
	}
	return s.fsm.ApplyLocal(cmd)
}

func (s *Service) ApplyPeerProposal(ctx context.Context, issuerNodeID string, cmd clusterModels.Command) (uint64, error) {
	speaksFor, ok := proposable[cmd.Type+"/"+cmd.Action]
	if !ok {
		return 0, fmt.Errorf("raft_command_not_proposable: %s/%s", cmd.Type, cmd.Action)
	}
	if speaksFor != nil {
		if node := speaksFor(cmd.Data); node == "" || node != strings.TrimSpace(issuerNodeID) {
			return 0, fmt.Errorf("raft_command_issuer_mismatch: %s/%s node_id=%s issuer=%s", cmd.Type, cmd.Action, node, issuerNodeID)
		}
	}
	return s.applyProposal(ctx, cmd)
}

// Forwarding callers look for the not_leader text. A proposal whose caller
// already gave up is dropped: a follower that timed out mustn't see it commit later.
func (s *Service) applyProposal(ctx context.Context, cmd clusterModels.Command) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("raft_proposal_abandoned: %w", err)
	}
	r := s.Raft()
	if r == nil || r.State() == raft.Shutdown {
		return 0, errConsensusUnavailable
	}
	if r.State() != raft.Leader {
		return 0, fmt.Errorf("not_leader: %w", raft.ErrNotLeader)
	}
	if err := s.requireMemberProtocol(r, s.fsm.Protocol(cmd.Type, cmd.Action)); err != nil {
		return 0, err
	}
	// Record provenance before sharing users, even if the upgrade's info job hasn't run yet.
	// Raft delivers the ID first, including to a member later removed while partitioned.
	if cmd.Type == "sylve_user" {
		if _, err := s.ensureClusterIdentity(ctx); err != nil {
			return 0, err
		}
	}
	if err := clusterModels.PrepareCommand(&cmd, time.Now()); err != nil {
		return 0, err
	}
	return s.raftApply(r, cmd)
}

func (s *Service) requireMemberProtocol(r *raft.Raft, need int) error {
	if need <= 1 {
		return nil
	}
	future := r.GetConfiguration()
	if err := future.Error(); err != nil {
		return fmt.Errorf("raft_configuration_unavailable: %w", err)
	}
	var nodes []clusterModels.ClusterNode
	if err := s.DB.Select("node_uuid", "protocol").Find(&nodes).Error; err != nil {
		return err
	}
	reported := make(map[string]int, len(nodes))
	for _, node := range nodes {
		reported[node.NodeUUID] = node.Protocol
	}
	for _, server := range future.Configuration().Servers {
		nodeID := string(server.ID)
		if nodeID == s.LocalNodeID() {
			continue
		}
		if speaks := max(reported[nodeID], 1); speaks < need {
			return fmt.Errorf("%w: node %s speaks %d, the command needs %d", ErrClusterProtocolOutdated, nodeID, speaks, need)
		}
	}
	return nil
}

func (s *Service) raftApply(r *raft.Raft, cmd clusterModels.Command) (uint64, error) {
	payload, err := json.Marshal(cmd)
	if err != nil {
		return 0, fmt.Errorf("failed_to_marshal_command: %w", err)
	}
	future := r.Apply(payload, raftApplyTimeout)
	if err := future.Error(); err != nil {
		// A transfer hands leadership over on purpose; callers retry as after a lost leadership.
		if errors.Is(err, raft.ErrLeadershipTransferInProgress) {
			err = fmt.Errorf("%w: %w", raft.ErrLeadershipLost, err)
		}
		return 0, fmt.Errorf("raft_apply_failed: %w", err)
	}
	if resp, ok := future.Response().(error); ok && resp != nil {
		return 0, fmt.Errorf("fsm_apply_failed: %w", resp)
	}
	return future.Index(), nil
}

func (s *Service) forwardProposal(ctx context.Context, r *raft.Raft, cmd clusterModels.Command) (uint64, error) {
	leaderAddress, _ := r.LeaderWithID()
	host := raftAddressHost(string(leaderAddress))
	if host == "" {
		return 0, errConsensusUnavailable
	}
	payload, err := json.Marshal(cmd)
	if err != nil {
		return 0, err
	}
	response, err := s.peerHTTP(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://%s/api/intra-cluster/raft/apply", ClusterAPIHost(host)),
		payload,
		map[string]string{"Accept": "application/json", "Content-Type": "application/json"},
		proposalForwardTimeout,
		1<<20,
	)
	if err != nil {
		return 0, fmt.Errorf("raft_proposal_forward_failed: %w", err)
	}
	var apiResponse internal.APIResponse[ProposalResult]
	if err := json.Unmarshal(response.Body, &apiResponse); err != nil {
		return 0, fmt.Errorf("raft_proposal_response_invalid: %w", err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 && apiResponse.Status == "success" {
		return apiResponse.Data.Index, nil
	}
	return 0, proposalError(apiResponse.Message, apiResponse.Error)
}

// The write is committed either way: if this node lags past the timeout, the
// caller just sees it a little later.
func (s *Service) WaitForLocalApply(ctx context.Context, index uint64) {
	ctx, cancel := context.WithTimeout(ctx, raftApplyTimeout)
	defer cancel()
	_, _ = s.WaitForAppliedIndex(ctx, index)
}

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
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
)

const (
	callTimeout     = 30 * time.Second
	CallNodeHeader  = "X-Sylve-Call-Node"
	CallIndexHeader = "X-Sylve-Call-Index"
)

type callFunc func(ctx context.Context, issuer string, payload json.RawMessage) (any, error)

type registeredCall struct {
	run    callFunc
	stream streamFunc
	leader bool
}

type CallResult struct {
	Index uint64          `json:"index,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

func OnLeader[Req, Resp any](s *Service, name string, fn func(ctx context.Context, issuer string, req Req) (Resp, error)) {
	register(s, name, true, fn)
}

func OnNode[Req, Resp any](s *Service, name string, fn func(ctx context.Context, issuer string, req Req) (Resp, error)) {
	register(s, name, false, fn)
}

func register[Req, Resp any](s *Service, name string, leader bool, fn func(context.Context, string, Req) (Resp, error)) {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	if s.calls == nil {
		s.calls = make(map[string]registeredCall)
	}
	s.calls[name] = registeredCall{leader: leader, run: func(ctx context.Context, issuer string, payload json.RawMessage) (any, error) {
		var req Req
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("call_request_invalid: %s: %w", name, err)
		}
		return fn(ctx, issuer, req)
	}}
}

func (s *Service) registeredCall(name string, leader bool) (callFunc, error) {
	s.callsOnce.Do(s.registerCalls)
	s.callsMu.RLock()
	defer s.callsMu.RUnlock()
	call, ok := s.calls[name]
	if !ok || call.leader != leader {
		return nil, fmt.Errorf("call_unknown: %s", name)
	}
	return call.run, nil
}

func runLocally[Resp any](ctx context.Context, call callFunc, issuer string, payload json.RawMessage) (Resp, error) {
	var resp Resp
	result, err := call(ctx, issuer, payload)
	if typed, ok := result.(Resp); ok {
		resp = typed
	}
	return resp, err
}

func decodeCallResult[Resp any](name string, result CallResult, err error) (Resp, error) {
	var resp Resp
	if len(result.Data) != 0 {
		if decodeErr := json.Unmarshal(result.Data, &resp); decodeErr != nil && err == nil {
			err = fmt.Errorf("call_response_invalid: %s: %w", name, decodeErr)
		}
	}
	return resp, err
}

func CallLeader[Req, Resp any](ctx context.Context, s *Service, name string, req Req) (Resp, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		var resp Resp
		return resp, fmt.Errorf("call_request_invalid: %s: %w", name, err)
	}
	r := s.Raft()
	if r == nil || r.State() == raft.Leader {
		call, err := s.registeredCall(name, true)
		if err != nil {
			var resp Resp
			return resp, err
		}
		return runLocally[Resp](ctx, call, s.LocalNodeID(), payload)
	}
	leaderAddress, _ := r.LeaderWithID()
	host := raftAddressHost(string(leaderAddress))
	if host == "" {
		var resp Resp
		return resp, errConsensusUnavailable
	}
	result, err := s.forwardCall(ctx, host, "leader", name, payload, nil)
	if result.Index != 0 {
		s.WaitForLocalApply(ctx, result.Index)
	}
	return decodeCallResult[Resp](name, result, err)
}

func CallNode[Req, Resp any](ctx context.Context, s *Service, nodeID, name string, req Req) (Resp, error) {
	return callNode[Req, Resp](ctx, s, nodeID, name, req, s.AppliedIndex())
}

// A call that must answer at once passes index 0: a readdress may run with no
// leader to bring the node up to date.
func callNode[Req, Resp any](ctx context.Context, s *Service, nodeID, name string, req Req, index uint64) (Resp, error) {
	var resp Resp
	payload, err := json.Marshal(req)
	if err != nil {
		return resp, fmt.Errorf("call_request_invalid: %s: %w", name, err)
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == s.LocalNodeID() {
		call, err := s.registeredCall(name, false)
		if err != nil {
			return resp, err
		}
		return runLocally[Resp](ctx, call, nodeID, payload)
	}
	host, err := s.nodeAPIHost(nodeID)
	if err != nil {
		return resp, err
	}
	return callNodeAt[Resp](ctx, s, host, nodeID, name, payload, index)
}

func callNodeAt[Resp any](ctx context.Context, s *Service, host, nodeID, name string, payload []byte, index uint64) (Resp, error) {
	result, err := s.forwardCall(ctx, host, "node", name, payload, map[string]string{
		CallNodeHeader:  nodeID,
		CallIndexHeader: strconv.FormatUint(index, 10),
	})
	return decodeCallResult[Resp](name, result, err)
}

func (s *Service) nodeAPIHost(nodeID string) (string, error) {
	if s.addressProvider != nil {
		if address, err := s.addressProvider.ServerAddr(raft.ServerID(nodeID)); err == nil {
			return raftAddressHost(string(address)), nil
		}
	}
	server, err := s.joinServer(nodeID)
	if err != nil {
		return "", err
	}
	if server == nil {
		return "", fmt.Errorf("call_node_unknown: %s: %w", nodeID, ErrNodeNotFound)
	}
	return raftAddressHost(string(server.Address)), nil
}

var (
	ErrNodeNotFound      = errors.New("node_not_found")
	ErrNodeNameAmbiguous = errors.New("node_name_ambiguous")
)

func (s *Service) MemberAPIHost(node string) (string, error) {
	var nodeIDs []string
	if err := s.DB.Model(&clusterModels.ClusterNode{}).
		Where("node_uuid = ? OR hostname = ?", node, node).
		Pluck("node_uuid", &nodeIDs).Error; err != nil {
		return "", err
	}
	if len(nodeIDs) > 1 {
		return "", ErrNodeNameAmbiguous
	}
	if len(nodeIDs) == 1 {
		node = nodeIDs[0]
	}
	host, err := s.nodeAPIHost(node)
	if err != nil {
		return "", err
	}
	return ClusterAPIHost(host), nil
}

func (s *Service) ServeLeaderCall(ctx context.Context, issuer, name string, payload json.RawMessage) (CallResult, error) {
	call, err := s.registeredCall(name, true)
	if err != nil {
		return CallResult{}, err
	}
	if r := s.Raft(); r == nil || r.State() != raft.Leader {
		return CallResult{}, fmt.Errorf("not_leader: %w", raft.ErrNotLeader)
	}
	resp, callErr := call(ctx, issuer, payload)
	var result CallResult
	// A node the call removed gets no more entries, so it isn't told to wait.
	if server, err := s.joinServer(issuer); err == nil && server != nil {
		result.Index = s.AppliedIndex()
	}
	if result.Data, err = json.Marshal(resp); err != nil && callErr == nil {
		callErr = fmt.Errorf("call_response_invalid: %s: %w", name, err)
	}
	return result, callErr
}

func (s *Service) ServeNodeCall(
	ctx context.Context,
	issuer, nodeID string,
	index uint64,
	name string,
	payload json.RawMessage,
) (CallResult, error) {
	call, err := s.registeredCall(name, false)
	if err != nil {
		return CallResult{}, err
	}
	return s.serveOnNode(ctx, nodeID, index, name, func() (any, error) {
		return call(ctx, issuer, payload)
	})
}

// serveOnNode runs a call addressed to nodeID once this node has applied the
// caller's index, so the call sees every write the caller made before it.
func (s *Service) serveOnNode(
	ctx context.Context,
	nodeID string,
	index uint64,
	name string,
	run func() (any, error),
) (CallResult, error) {
	if local := s.LocalNodeID(); strings.TrimSpace(nodeID) != local {
		return CallResult{}, fmt.Errorf("call_node_mismatch: for=%s here=%s", nodeID, local)
	}
	waitCtx, cancel := context.WithTimeout(ctx, callTimeout)
	_, err := s.WaitForAppliedIndex(waitCtx, index)
	cancel()
	if err != nil {
		return CallResult{}, err
	}
	resp, callErr := run()
	var result CallResult
	if result.Data, err = json.Marshal(resp); err != nil && callErr == nil {
		callErr = fmt.Errorf("call_response_invalid: %s: %w", name, err)
	}
	return result, callErr
}

func (s *Service) forwardCall(
	ctx context.Context,
	host, kind, name string,
	payload []byte,
	extraHeaders map[string]string,
) (CallResult, error) {
	headers := map[string]string{"Accept": "application/json", "Content-Type": "application/json"}
	for key, value := range extraHeaders {
		headers[key] = value
	}
	timeout := callTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	response, err := s.peerHTTP(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://%s/api/intra-cluster/%s/%s", ClusterAPIHost(host), kind, url.PathEscape(name)),
		payload,
		headers,
		timeout,
		4<<20,
	)
	if err != nil {
		return CallResult{}, fmt.Errorf("call_forward_failed: %s: %w", name, err)
	}
	return callResult(name, response)
}

func callResult(name string, response utils.HTTPReadResponse) (CallResult, error) {
	var apiResponse internal.APIResponse[CallResult]
	if err := json.Unmarshal(response.Body, &apiResponse); err != nil {
		return CallResult{}, fmt.Errorf("call_response_invalid: %s: status=%d: %w", name, response.StatusCode, err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 && apiResponse.Status == "success" {
		return apiResponse.Data, nil
	}
	return apiResponse.Data, proposalError(apiResponse.Message, apiResponse.Error)
}

func (s *Service) registerCalls() {
	OnNode(s, "join-progress", func(context.Context, string, struct{}) (ClusterJoinProgress, error) {
		return s.LocalJoinProgress()
	})
	OnNode(s, "sync-health", func(_ context.Context, _ string, nodes []clusterServiceInterfaces.NodeHealthSync) (struct{}, error) {
		return struct{}{}, s.SyncClusterHealth(nodes)
	})
	OnNode(s, "left-panel-refresh", func(_ context.Context, _ string, req leftPanelRefreshSyncRequest) (struct{}, error) {
		s.EmitLeftPanelRefreshLocal(req.Reason)
		return struct{}{}, nil
	})
	OnNode(s, "replicated-state", func(context.Context, string, struct{}) (ReplicatedStateDigest, error) {
		return s.LocalReplicatedStateDigest("")
	})
	OnNode(s, "backup-job-safety-validation", func(ctx context.Context, _ string, req BackupJobSafetyValidationRequest) (BackupJobSafetyValidationResult, error) {
		return s.ValidateBackupJobSafetyLocal(ctx, req)
	})
	OnNode(s, "backup-target-validation", func(ctx context.Context, _ string, req BackupTargetValidationRequest) (clusterModels.BackupTargetNodeReadinessUpdate, error) {
		return s.ValidateBackupTargetConnectivityLocal(ctx, req)
	})
	OnNode(s, "guest-identity-inventory", func(ctx context.Context, _ string, _ struct{}) (GuestIdentityInventorySnapshot, error) {
		return s.LocalGuestIdentityInventory(ctx)
	})
	OnNode(s, "readdress-identity", func(context.Context, string, struct{}) (ReaddressIdentity, error) {
		return s.LocalReaddressIdentity()
	})
	OnNode(s, "readdress-member", func(ctx context.Context, issuer string, req MemberAddressChangeRequest) (struct{}, error) {
		return struct{}{}, s.CommitMemberAddress(ctx, req, issuer)
	})
	OnLeader(s, "remove-peer", s.removePeerCall)
	OnLeader(s, "backup-target-host-key", s.installBackupTargetHostKeyCall)
	OnLeader(s, "replication-run-claim", s.replicationRunClaimCall)
	OnLeader(s, "backup-job-operation", s.backupJobOperationCall)
	OnLeader(s, "backup-target-restore-operation", s.backupTargetRestoreOperationCall)
	OnLeader(s, "replication-guest-operation", s.replicationGuestOperationCall)
	OnLeader(s, "backup-job-runner-rebind-prepare", s.backupJobRunnerRebindPrepareCall)
	OnLeader(s, "replication-target-readiness", func(_ context.Context, _ string, update clusterModels.ReplicationTargetReadinessUpdate) (struct{}, error) {
		return struct{}{}, s.UpdateReplicationTargetReadiness(update)
	})
	OnLeader(s, "backup-job-state", func(_ context.Context, _ string, update BackupJobRuntimeStateUpdate) (struct{}, error) {
		return struct{}{}, s.UpdateBackupJobRuntimeState(update)
	})
	OnLeader(s, "replication-policy-state", func(_ context.Context, _ string, update ReplicationPolicyRuntimeState) (struct{}, error) {
		return struct{}{}, s.ProposeReplicationPolicyStateUpdate(update)
	})
	OnLeader(s, "backup-job-friendly-source", func(_ context.Context, _ string, update BackupJobFriendlySourceUpdate) (struct{}, error) {
		return struct{}{}, s.SyncBackupJobFriendlySourceByGuest(update)
	})
}

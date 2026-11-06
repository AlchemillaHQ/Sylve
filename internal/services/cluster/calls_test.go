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
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
)

func TestServeLeaderCallRunsOnlyKnownCallsOnTheLeader(t *testing.T) {
	service := withFSM(&Service{DB: newClusterServiceTestDB(t)})
	if _, err := service.ServeLeaderCall(t.Context(), "node-2", "no-such-call", nil); err == nil ||
		!strings.Contains(err.Error(), "call_unknown") {
		t.Fatalf("unknown call: %v", err)
	}
	if _, err := service.ServeLeaderCall(t.Context(), "node-2", "replication-run-claim", nil); !errors.Is(err, raft.ErrNotLeader) {
		t.Fatalf("call on a node that doesn't lead: %v", err)
	}
}

func TestIntegrationLeaderCallKeepsARefusalAndItsData(t *testing.T) {
	nodes, leader, _ := newLeaveTestCluster(t, 3)
	target := leaveTestFollowers(nodes, leader)[0]
	if err := leader.service.DB.Create(&clusterModels.BackupJob{
		ID: 5, Name: "runs-on-target", TargetID: 1, RunnerNodeID: target.id,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data", CronExpr: "0 * * * *", Enabled: true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	err := target.service.requestRemoval(t.Context())
	var blocked *PeerRemovalBlockedError
	if !errors.As(err, &blocked) || blocked.Conflict.NodeID != target.id || len(blocked.Conflict.Dependencies) != 1 ||
		blocked.Conflict.Dependencies[0].Kind != PeerRemovalDependencyBackupJob {
		t.Fatalf("removal = %v (%+v), want the backup job as the conflict", err, blocked)
	}
	requireAdmitted(t, target.service, true)
}

func newRunClaimTestService(t *testing.T) (*Service, clusterModels.ReplicationPolicy) {
	t.Helper()
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationRunOperation{})
	nextRunAt := time.Date(2026, time.August, 2, 7, 30, 0, 0, time.UTC)
	policy := clusterModels.ReplicationPolicy{
		ID: 801, Name: "manual-claim", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 107, SourceNodeID: "node-owner", ActiveNodeID: "node-owner",
		OwnerEpoch: 2, Enabled: true, ProtectionState: clusterModels.ReplicationProtectionStateArmed,
		ScheduleRevision: 4, NextRunAt: &nextRunAt,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	return withFSM(&Service{DB: db}), policy
}

func TestLeaderCallRunClaimIsIdempotent(t *testing.T) {
	service, policy := newRunClaimTestService(t)
	decidedAt := time.Date(2026, time.August, 2, 7, 1, 0, 0, time.UTC)
	decision := clusterModels.ReplicationPolicyScheduleDecision{
		PolicyID: policy.ID, ExpectedScheduleRevision: policy.ScheduleRevision,
		ExpectedOwnerEpoch: policy.OwnerEpoch, ExpectedNextRunAt: policy.NextRunAt,
		NextRunAt: policy.NextRunAt, DecidedAt: decidedAt,
		ClaimToken: "replication:node-owner:manual-claim", HolderNodeID: "node-owner",
		OccurrenceAt: &decidedAt,
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := CallLeader[clusterModels.ReplicationPolicyScheduleDecision, struct{}](
			t.Context(), service, "replication-run-claim", decision,
		); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}

	var operations []clusterModels.ReplicationRunOperation
	if err := service.DB.Find(&operations, "policy_id = ?", policy.ID).Error; err != nil || len(operations) != 1 ||
		operations[0].Token != decision.ClaimToken || operations[0].HolderNodeID != decision.HolderNodeID ||
		operations[0].State != clusterModels.ReplicationRunOperationQueued {
		t.Fatalf("operations = %+v, %v", operations, err)
	}
	var updated clusterModels.ReplicationPolicy
	if err := service.DB.First(&updated, policy.ID).Error; err != nil || updated.ScheduleRevision != policy.ScheduleRevision+1 ||
		updated.NextRunAt == nil || !updated.NextRunAt.Equal(*policy.NextRunAt) {
		t.Fatalf("policy after the claim = %+v, %v", updated, err)
	}
}

func TestLeaderCallRunClaimRefusesARuntimeUpdate(t *testing.T) {
	service, policy := newRunClaimTestService(t)
	decidedAt := time.Date(2026, time.August, 2, 7, 1, 0, 0, time.UTC)
	_, err := CallLeader[clusterModels.ReplicationPolicyScheduleDecision, struct{}](
		t.Context(), service, "replication-run-claim", clusterModels.ReplicationPolicyScheduleDecision{
			PolicyID: policy.ID, ExpectedOwnerEpoch: policy.OwnerEpoch, DecidedAt: decidedAt,
			ClaimToken: "replication:node-owner:forged", HolderNodeID: "node-owner", OccurrenceAt: &decidedAt,
			SetRuntime: true, LastStatus: "success",
		},
	)
	if err == nil || !strings.Contains(err.Error(), "replication_run_claim_invalid") {
		t.Fatalf("runtime update through the claim call: %v", err)
	}
	var count int64
	if err := service.DB.Model(&clusterModels.ReplicationRunOperation{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("operations after the refusal = %d, %v", count, err)
	}
}

func TestLeaderCallBackupJobStateKeepsEncryptionOfAnUnversionedUpdate(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.BackupJob{})
	service := withFSM(&Service{DB: db})
	job := clusterModels.BackupJob{
		ID: 71, Name: "forwarded-state", TargetID: 9,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data",
		CronExpr: "0 0 * * *", Enabled: true, Encrypted: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		payload   string
		encrypted bool
	}{
		{`{"jobId":71,"lastStatus":"failed","lastError":"older follower"}`, true},
		{`{"version":1,"jobId":71,"lastStatus":"success","encrypted":false}`, false},
	} {
		if _, err := CallLeader[json.RawMessage, struct{}](t.Context(), service, "backup-job-state", json.RawMessage(step.payload)); err != nil {
			t.Fatalf("%s: %v", step.payload, err)
		}
		if err := db.First(&job, job.ID).Error; err != nil || job.Encrypted != step.encrypted {
			t.Fatalf("after %s: encrypted=%t, %v; want %t", step.payload, job.Encrypted, err, step.encrypted)
		}
	}
}

func TestLeaderCallReplicationTargetReadiness(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{})
	if err := db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "readiness", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 10,
		ActiveNodeID: "node-1", OwnerEpoch: 7, Enabled: true,
		ProtectionState: clusterModels.ReplicationProtectionStateInitializing,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&clusterModels.ReplicationPolicyTarget{PolicyID: 1, NodeID: "node-2", Weight: 100}).Error; err != nil {
		t.Fatal(err)
	}
	service := withFSM(&Service{DB: db})
	publish := func(update clusterModels.ReplicationTargetReadinessUpdate) error {
		_, err := CallLeader[clusterModels.ReplicationTargetReadinessUpdate, struct{}](t.Context(), service, "replication-target-readiness", update)
		return err
	}
	target := func() clusterModels.ReplicationPolicyTarget {
		var target clusterModels.ReplicationPolicyTarget
		if err := db.Where("policy_id = ? AND node_id = ?", 1, "node-2").First(&target).Error; err != nil {
			t.Fatal(err)
		}
		return target
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	readyUntil := now.Add(time.Hour)
	update := clusterModels.ReplicationTargetReadinessUpdate{
		PolicyID: 1, NodeID: "node-2", ExpectedOwnerEpoch: 7, EvaluatedAt: now,
		Ready: true, GenerationID: "gen-7", ManifestHash: "hash-7",
		RequiredDatasetCount: 2, CompletedDatasetCount: 2, LastVerifiedAt: &now, ReadyUntil: &readyUntil,
	}
	if err := publish(update); err != nil {
		t.Fatal(err)
	}
	if got := target(); !got.Ready || got.OwnerEpoch != 7 || got.GenerationID != "gen-7" {
		t.Fatalf("readiness = %+v", got)
	}

	stale := update
	stale.ExpectedOwnerEpoch, stale.GenerationID = 6, "stale-generation"
	if err := publish(stale); err == nil {
		t.Fatal("a stale owner epoch was accepted")
	}
	missing := update
	missing.NodeID = "node-missing"
	if err := publish(missing); err == nil {
		t.Fatal("readiness for a target the policy doesn't have was accepted")
	}
	if got := target(); got.GenerationID != "gen-7" {
		t.Fatalf("refused updates changed readiness: %+v", got)
	}
}

func TestIntegrationLeaderCallRemovesOnlyTheCaller(t *testing.T) {
	nodes, leader, _ := newLeaveTestCluster(t, 3)
	followers := leaveTestFollowers(nodes, leader)
	payload, err := json.Marshal(RemoveMembershipRequest{NodeID: followers[1].id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leader.service.ServeLeaderCall(t.Context(), followers[0].id, "remove-peer", payload); err == nil ||
		!strings.Contains(err.Error(), "cluster_leave_issuer_mismatch") {
		t.Fatalf("removing another node: %v", err)
	}
	if server, err := leader.service.joinServer(followers[1].id); err != nil || server == nil {
		t.Fatalf("the other node's membership = %+v, %v", server, err)
	}

	payload, err = json.Marshal(RemoveMembershipRequest{NodeID: followers[0].id})
	if err != nil {
		t.Fatal(err)
	}
	result, err := leader.service.ServeLeaderCall(t.Context(), followers[0].id, "remove-peer", payload)
	if err != nil || result.Index != 0 {
		t.Fatalf("removing the caller = %+v, %v; want no index", result, err)
	}
}

func TestIntegrationNodeCallSeesTheCallersWrites(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, &clusterModels.ClusterNote{})
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	routeTestPeerRequests(nodes)
	follower := nodes[0]
	if follower == leader {
		follower = nodes[1]
	}
	OnNode(follower.service, "test-count-notes", func(context.Context, string, struct{}) (int64, error) {
		var count int64
		err := follower.service.DB.Model(&clusterModels.ClusterNote{}).Count(&count).Error
		return count, err
	})

	follower.transport.DisconnectAll()
	for _, node := range nodes {
		node.transport.Disconnect(follower.addr)
	}
	if err := leader.service.ProposeNoteCreate(t.Context(), "written-first", "content"); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		connectClusterRaftTestNodes(nodes)
	}()
	count, err := CallNode[struct{}, int64](t.Context(), leader.service, follower.id, "test-count-notes", struct{}{})
	if err != nil || count != 1 {
		t.Fatalf("notes the follower saw = %d, %v; want the leader's note", count, err)
	}

	if _, err := follower.service.ServeNodeCall(t.Context(), leader.id, leader.id, 0, "test-count-notes", []byte("{}")); err == nil ||
		!strings.Contains(err.Error(), "call_node_mismatch") {
		t.Fatalf("a call meant for another node: %v", err)
	}
	if _, err := CallNode[struct{}, int64](t.Context(), leader.service, "no-such-node", "test-count-notes", struct{}{}); err == nil ||
		!strings.Contains(err.Error(), "call_node_unknown") {
		t.Fatalf("a call to an unknown node: %v", err)
	}
}

func routeNodeCalls(caller *Service, targets ...*Service) {
	caller.addressProvider = newRaftAddressProvider()
	byHost := make(map[string]*Service, len(targets))
	for _, target := range targets {
		caller.addressProvider.set(raft.ServerID(target.NodeID), raft.ServerAddress(target.NodeID))
		byHost[target.NodeID] = target
	}
	caller.peerRequest = func(ctx context.Context, _, rawURL string, payload []byte, headers map[string]string) (int, []byte, error) {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return 0, nil, err
		}
		target := byHost[parsed.Hostname()]
		name, ok := strings.CutPrefix(parsed.Path, "/api/intra-cluster/node/")
		if target == nil || !ok {
			return 0, nil, fmt.Errorf("unexpected request %s", rawURL)
		}
		index, _ := strconv.ParseUint(headers[CallIndexHeader], 10, 64)
		result, err := target.ServeNodeCall(ctx, caller.LocalNodeID(), headers[CallNodeHeader], index, name, payload)
		response := internal.APIResponse[CallResult]{Status: "success", Data: result}
		status := http.StatusOK
		if err != nil {
			status = http.StatusConflict
			response.Status, response.Message, response.Error = "error", ProposalErrorCode(err), err.Error()
		}
		body, err := json.Marshal(response)
		return status, body, err
	}
}

func TestMemberAPIHostNamesOneMember(t *testing.T) {
	database := newClusterServiceTestDB(t, &clusterModels.ClusterNode{})
	for _, node := range []clusterModels.ClusterNode{
		{NodeUUID: "node-b", Hostname: "hera"},
		{NodeUUID: "node-c", Hostname: "twin"},
		{NodeUUID: "node-d", Hostname: "twin"},
		{NodeUUID: "node-e", Hostname: "gone"},
	} {
		if err := database.Create(&node).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{DB: database, addressProvider: newRaftAddressProvider()}
	service.addressProvider.set("node-b", raft.ServerAddress(RaftServerAddress("10.0.0.2")))
	service.addressProvider.set("node-f", raft.ServerAddress(RaftServerAddress("10.0.0.6")))

	for _, node := range []string{"node-b", "hera"} {
		if host, err := service.MemberAPIHost(node); err != nil || host != ClusterAPIHost("10.0.0.2") {
			t.Fatalf("%s is at %q, %v", node, host, err)
		}
	}
	if host, err := service.MemberAPIHost("node-f"); err != nil || host != ClusterAPIHost("10.0.0.6") {
		t.Fatalf("node-f is at %q, %v", host, err)
	}
	for node, want := range map[string]error{
		"twin":   ErrNodeNameAmbiguous,
		"nobody": ErrNodeNotFound,
		"gone":   ErrNodeNotFound,
	} {
		if host, err := service.MemberAPIHost(node); !errors.Is(err, want) {
			t.Fatalf("%s is at %q, %v; want %v", node, host, err, want)
		}
	}
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

type replicationPolicyDeleteCleanupStub struct {
	cleanup func(context.Context, uint, uint64) error
}

func newSingleNodeReplicationHandlerRaft(t *testing.T, database *gorm.DB) *raft.Raft {
	t.Helper()
	fsm := clusterModels.NewFSMDispatcher(database)
	clusterModels.RegisterDefaultHandlers(fsm)
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID("node-a")
	config.Logger = hclog.NewNullLogger()
	config.HeartbeatTimeout = 100 * time.Millisecond
	config.ElectionTimeout = 100 * time.Millisecond
	config.LeaderLeaseTimeout = 50 * time.Millisecond
	config.CommitTimeout = 10 * time.Millisecond
	address, transport := raft.NewInmemTransport(raft.ServerAddress("node-a"))
	instance, err := raft.NewRaft(
		config,
		fsm,
		raft.NewInmemStore(),
		raft.NewInmemStore(),
		raft.NewInmemSnapshotStore(),
		transport,
	)
	if err != nil {
		t.Fatalf("create raft: %v", err)
	}
	if err := instance.BootstrapCluster(raft.Configuration{Servers: []raft.Server{{
		ID: raft.ServerID("node-a"), Address: address, Suffrage: raft.Voter,
	}}}).Error(); err != nil {
		t.Fatalf("bootstrap raft: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for instance.State() != raft.Leader && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if instance.State() != raft.Leader {
		t.Fatalf("raft state=%s, want leader", instance.State())
	}
	t.Cleanup(func() {
		if instance.State() != raft.Shutdown {
			_ = instance.Shutdown().Error()
		}
		_ = transport.Close()
	})
	return instance
}

func (s *replicationPolicyDeleteCleanupStub) CleanupReplicationPolicyDeleteBestEffort(
	ctx context.Context,
	policyID uint,
	minimumRaftAppliedIndex uint64,
) error {
	if s == nil || s.cleanup == nil {
		return nil
	}
	return s.cleanup(ctx, policyID, minimumRaftAppliedIndex)
}

func newReplicationRouter(cS *cluster.Service) *gin.Engine {
	return newReplicationRouterWithDeleteCleanup(cS, nil)
}

func newReplicationRouterWithDeleteCleanup(
	cS *cluster.Service,
	cleanupService replicationPolicyDeleteCleanupService,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/cluster/replication/policies", ReplicationPolicies(cS))
	r.POST("/cluster/replication/policies", CreateReplicationPolicy(cS))
	r.PUT("/cluster/replication/policies/:id", UpdateReplicationPolicy(cS, nil))
	r.DELETE("/cluster/replication/policies/:id", DeleteReplicationPolicy(cS, cleanupService))
	r.GET("/cluster/replication/events", ReplicationEvents(cS))
	r.GET("/cluster/replication/events/:id", ReplicationEventByID(cS))
	return r
}

func newReplicationInternalRouter(cS *cluster.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/intra/replication-runtime-state", ReplicationPolicyRuntimeStateInternal(cS, nil))
	r.POST("/intra/replication-run-claim", ReplicationRunClaimInternal(cS))
	r.POST("/intra/activate", ActivateReplicationPolicyInternal(cS, nil))
	r.POST("/intra/demote", DemoteReplicationPolicyInternal(cS, nil))
	r.POST("/intra/catchup", CatchupReplicationPolicyInternal(cS, nil))
	r.POST("/intra/replication-target-readiness", UpdateReplicationTargetReadinessInternal(cS))
	r.POST("/intra/cleanup-policy-delete", CleanupReplicationPolicyDeleteInternal(cS, nil))
	r.POST("/intra/replication-guest-operation-status", ReplicationGuestOperationStatusInternal(cS))
	return r
}

func TestReplicationRunClaimInternalAppliesOneExactIdempotentClaim(t *testing.T) {
	db := newClusterHandlerTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationRunOperation{},
	)
	nextRunAt := time.Date(2026, time.August, 2, 7, 30, 0, 0, time.UTC)
	policy := clusterModels.ReplicationPolicy{
		ID: 801, Name: "manual-claim", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 107, SourceNodeID: "node-owner", ActiveNodeID: "node-owner",
		OwnerEpoch: 2, Enabled: true, ProtectionState: clusterModels.ReplicationProtectionStateArmed,
		ScheduleRevision: 4, NextRunAt: &nextRunAt,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	decidedAt := time.Date(2026, time.August, 2, 7, 1, 0, 0, time.UTC)
	occurrenceAt := decidedAt
	decision := clusterModels.ReplicationPolicyScheduleDecision{
		PolicyID: policy.ID, ExpectedScheduleRevision: policy.ScheduleRevision,
		ExpectedOwnerEpoch: policy.OwnerEpoch, ExpectedNextRunAt: &nextRunAt,
		NextRunAt: &nextRunAt, DecidedAt: decidedAt,
		ClaimToken: "replication:node-owner:manual-claim", HolderNodeID: "node-owner",
		OccurrenceAt: &occurrenceAt,
	}
	body, err := json.Marshal(decision)
	if err != nil {
		t.Fatalf("marshal claim: %v", err)
	}
	r := newReplicationInternalRouter(&cluster.Service{DB: db})

	for attempt := 1; attempt <= 2; attempt++ {
		response := performJSONRequest(t, r, http.MethodPost, "/intra/replication-run-claim", body)
		if response.Code != http.StatusOK {
			t.Fatalf("attempt %d status=%d body=%s", attempt, response.Code, response.Body.String())
		}
	}

	var operation clusterModels.ReplicationRunOperation
	if err := db.First(&operation, "policy_id = ?", policy.ID).Error; err != nil {
		t.Fatalf("load operation: %v", err)
	}
	if operation.Token != decision.ClaimToken || operation.HolderNodeID != decision.HolderNodeID ||
		operation.State != clusterModels.ReplicationRunOperationQueued {
		t.Fatalf("unexpected operation: %+v", operation)
	}
	var operationCount int64
	if err := db.Model(&clusterModels.ReplicationRunOperation{}).Count(&operationCount).Error; err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if operationCount != 1 {
		t.Fatalf("operation count=%d, want 1", operationCount)
	}
	var updated clusterModels.ReplicationPolicy
	if err := db.First(&updated, policy.ID).Error; err != nil {
		t.Fatalf("reload policy: %v", err)
	}
	if updated.ScheduleRevision != policy.ScheduleRevision+1 || updated.NextRunAt == nil ||
		!updated.NextRunAt.Equal(nextRunAt) {
		t.Fatalf("manual claim changed schedule incorrectly: %+v", updated)
	}
}

func TestReplicationRunClaimInternalRejectsNonClaimRuntimeMutation(t *testing.T) {
	db := newClusterHandlerTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationRunOperation{},
	)
	r := newReplicationInternalRouter(&cluster.Service{DB: db})
	body := []byte(`{
		"policyId":801,
		"expectedOwnerEpoch":2,
		"decidedAt":"2026-08-02T07:01:00Z",
		"claimToken":"replication:node-owner:forged",
		"holderNodeId":"node-owner",
		"occurrenceAt":"2026-08-02T07:01:00Z",
		"setRuntime":true,
		"lastStatus":"success"
	}`)
	response := performJSONRequest(t, r, http.MethodPost, "/intra/replication-run-claim", body)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", response.Code, response.Body.String())
	}
}

func TestReplicationPolicyEnqueueErrorsExposeRetryableAvailability(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
	}{
		{
			name:       "follower leader discovery",
			err:        errors.New("replication_run_claim_unavailable: leader_not_available"),
			wantStatus: http.StatusServiceUnavailable, wantMessage: "replication_policy_enqueue_failed",
		},
		{
			name:       "raw raft follower",
			err:        errors.New("not_leader"),
			wantStatus: http.StatusServiceUnavailable, wantMessage: "replication_policy_enqueue_failed",
		},
		{
			name:       "follower apply lag duplicate",
			err:        errors.New("replication_policy_schedule_revision_conflict"),
			wantStatus: http.StatusConflict, wantMessage: "replication_policy_enqueue_failed",
		},
		{
			name:       "durable operation exists",
			err:        errors.New("replication_policy_already_running"),
			wantStatus: http.StatusConflict, wantMessage: "replication_policy_already_running",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, message := replicationPolicyEnqueueErrorResponse(test.err)
			if status != test.wantStatus || message != test.wantMessage {
				t.Fatalf("status=%d message=%q, want %d %q", status, message, test.wantStatus, test.wantMessage)
			}
		})
	}
}

func TestReplicationGuestOperationStatusRequiresExactAppliedRow(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ReplicationGuestOperation{})
	operation := clusterModels.ReplicationGuestOperation{
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 901,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		State:     clusterModels.ReplicationGuestOperationCutover,
		Token:     "migration:node-a:901", OwnerNodeID: "node-a", TargetNodeID: "node-b", TaskID: 901,
		AcquiredAt: time.Now().UTC(),
	}
	if err := db.Create(&operation).Error; err != nil {
		t.Fatalf("seed guest operation: %v", err)
	}
	r := newReplicationInternalRouter(&cluster.Service{DB: db})

	exact := []byte(`{"guestType":"vm","guestId":901,"operation":"migration","state":"cutover","token":"migration:node-a:901","targetNodeId":"node-b"}`)
	response := performJSONRequest(t, r, http.MethodPost, "/intra/replication-guest-operation-status", exact)
	if response.Code != http.StatusOK {
		t.Fatalf("exact applied row was rejected: status=%d body=%s", response.Code, response.Body.String())
	}

	stale := []byte(`{"guestType":"vm","guestId":901,"operation":"migration","state":"cutover","token":"stale-token","targetNodeId":"node-b"}`)
	response = performJSONRequest(t, r, http.MethodPost, "/intra/replication-guest-operation-status", stale)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale token was accepted: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestReplicationInternalTransitionPayloadValidation(t *testing.T) {
	r := newReplicationInternalRouter(nil)

	invalid := []struct {
		name string
		path string
		body string
	}{
		{
			name: "runtime state requires epoch and run",
			path: "/intra/replication-runtime-state",
			body: `{"policyId":1}`,
		},
		{
			name: "activate requires desired running",
			path: "/intra/activate",
			body: `{"policyId":1,"ownerEpoch":2,"transitionRunId":"run-1"}`,
		},
		{
			name: "demote requires transition run",
			path: "/intra/demote",
			body: `{"policyId":1,"ownerEpoch":2}`,
		},
		{
			name: "catchup requires generation",
			path: "/intra/catchup",
			body: `{"policyId":1,"targetNodeId":"node-2","ownerEpoch":2,"transitionRunId":"run-1"}`,
		},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			rr := performJSONRequest(t, r, http.MethodPost, tt.path, []byte(tt.body))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}

	valid := []struct {
		name string
		path string
		body string
	}{
		{
			name: "runtime state",
			path: "/intra/replication-runtime-state",
			body: `{"policyId":1,"ownerEpoch":2,"transitionRunId":"run-1"}`,
		},
		{
			name: "activate preserves false desired state",
			path: "/intra/activate",
			body: `{"policyId":1,"ownerEpoch":2,"transitionRunId":"run-1","desiredRunning":false}`,
		},
		{
			name: "demote",
			path: "/intra/demote",
			body: `{"policyId":1,"ownerEpoch":2,"transitionRunId":"run-1"}`,
		},
		{
			name: "catchup",
			path: "/intra/catchup",
			body: `{"policyId":1,"targetNodeId":"node-2","ownerEpoch":2,"transitionRunId":"run-1","generationId":"gen-2"}`,
		},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			rr := performJSONRequest(t, r, http.MethodPost, tt.path, []byte(tt.body))
			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("expected validated request to reach unavailable service (503), got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestReplicationDeleteCleanupInternalRequiresEpochAndService(t *testing.T) {
	r := newReplicationInternalRouter(nil)

	for _, body := range []string{
		`{"policyId":1}`,
		`{"expectedOwnerEpoch":2}`,
		`{"policyId":1,"expectedOwnerEpoch":0}`,
	} {
		rr := performJSONRequest(t, r, http.MethodPost, "/intra/cleanup-policy-delete", []byte(body))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for incomplete cleanup authority, got %d: %s", rr.Code, rr.Body.String())
		}
	}

	rr := performJSONRequest(
		t,
		r,
		http.MethodPost,
		"/intra/cleanup-policy-delete",
		[]byte(`{"policyId":1,"expectedOwnerEpoch":2}`),
	)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected validated cleanup request to require the service, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestReplicationDeleteCleanupInternalMapsFenceFailureAndAuthorityConflict(t *testing.T) {
	db := newClusterHandlerTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
	)
	policy := clusterModels.ReplicationPolicy{
		ID: 18, Name: "cleanup-authority", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 18, SourceNodeID: "node-a", ActiveNodeID: "node-a", OwnerEpoch: 3,
		ProtectionState: clusterModels.ReplicationProtectionStateDeleting,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	cS := &cluster.Service{DB: db, NodeID: "node-a"}
	zS := &zelta.Service{DB: db, Cluster: cS}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/intra/cleanup-policy-delete", CleanupReplicationPolicyDeleteInternal(cS, zS))

	fenceFailure := performJSONRequest(
		t,
		r,
		http.MethodPost,
		"/intra/cleanup-policy-delete",
		[]byte(`{"policyId":18,"expectedOwnerEpoch":3,"minimumRaftAppliedIndex":9}`),
	)
	if fenceFailure.Code != http.StatusServiceUnavailable ||
		!strings.Contains(fenceFailure.Body.String(), "cleanup_replication_policy_delete_applied_index_unavailable") {
		t.Fatalf("fence failure response=%d %s", fenceFailure.Code, fenceFailure.Body.String())
	}

	authorityConflict := performJSONRequest(
		t,
		r,
		http.MethodPost,
		"/intra/cleanup-policy-delete",
		[]byte(`{"policyId":18,"expectedOwnerEpoch":2,"minimumRaftAppliedIndex":0}`),
	)
	if authorityConflict.Code != http.StatusConflict ||
		!strings.Contains(authorityConflict.Body.String(), "epoch_mismatch") {
		t.Fatalf("authority conflict response=%d %s", authorityConflict.Code, authorityConflict.Body.String())
	}
}

func TestReplicationDeleteCleanupInternalRejectsStaleEpochAfterFence(t *testing.T) {
	db := newClusterHandlerTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
	)
	policy := clusterModels.ReplicationPolicy{
		ID: 181, Name: "fenced-authority", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 181, SourceNodeID: "node-a", ActiveNodeID: "node-a", OwnerEpoch: 4,
		ProtectionState: clusterModels.ReplicationProtectionStateDeleting,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	raftInstance := newSingleNodeReplicationHandlerRaft(t, db)
	cS := &cluster.Service{DB: db, NodeID: "node-a", Raft: raftInstance}
	if err := cS.UpdateReplicationPolicyProtectionState(
		policy.ID,
		policy.OwnerEpoch,
		clusterModels.ReplicationProtectionStateDeleting,
		false,
	); err != nil {
		t.Fatalf("apply lifecycle fence: %v", err)
	}
	minimum := raftInstance.AppliedIndex()
	if minimum == 0 {
		t.Fatal("raft fence index is zero")
	}
	zS := &zelta.Service{DB: db, Cluster: cS}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/intra/cleanup-policy-delete", CleanupReplicationPolicyDeleteInternal(cS, zS))
	body := []byte(fmt.Sprintf(
		`{"policyId":181,"expectedOwnerEpoch":3,"minimumRaftAppliedIndex":%d}`,
		minimum,
	))
	response := performJSONRequest(t, r, http.MethodPost, "/intra/cleanup-policy-delete", body)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "epoch_mismatch") {
		t.Fatalf("stale authority response=%d %s", response.Code, response.Body.String())
	}
	var retained clusterModels.ReplicationPolicy
	if err := db.First(&retained, policy.ID).Error; err != nil {
		t.Fatalf("stale authority removed policy: %v", err)
	}
}

func TestReplicationDeleteDoesNotRemoveMetadataWithoutCleanupService(t *testing.T) {
	db := newClusterHandlerTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationEvent{},
		&clusterModels.ReplicationTransitionEvent{},
	)
	policy := clusterModels.ReplicationPolicy{
		ID:              19,
		Name:            "delete-ack-barrier",
		GuestType:       clusterModels.ReplicationGuestTypeVM,
		GuestID:         901,
		ActiveNodeID:    "node-1",
		OwnerEpoch:      4,
		Enabled:         true,
		ProtectionState: clusterModels.ReplicationProtectionStateArmed,
		TransitionState: clusterModels.ReplicationTransitionStateNone,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	policyID := policy.ID
	now := time.Now().UTC()
	if err := db.Create(&clusterModels.ReplicationEvent{
		PolicyID: &policyID, EventType: "replication", Status: "failed", StartedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed local event: %v", err)
	}
	if err := db.Create(&clusterModels.ReplicationTransitionEvent{
		PolicyID: &policyID, TransitionRunID: "failed-delete", EventType: "failover", Status: "failed", StartedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed transition event: %v", err)
	}

	cS := &cluster.Service{DB: db}
	r := newReplicationRouter(cS)
	rr := performJSONRequest(t, r, http.MethodDelete, "/cluster/replication/policies/19", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without cleanup service, got %d: %s", rr.Code, rr.Body.String())
	}

	var retained clusterModels.ReplicationPolicy
	if err := db.First(&retained, policy.ID).Error; err != nil {
		t.Fatalf("policy metadata was removed without cleanup acknowledgement: %v", err)
	}
	if retained.ProtectionState != clusterModels.ReplicationProtectionStateArmed {
		t.Fatalf("policy lifecycle changed before cleanup was available: %s", retained.ProtectionState)
	}
	for name, model := range map[string]any{
		"local":      &clusterModels.ReplicationEvent{},
		"transition": &clusterModels.ReplicationTransitionEvent{},
	} {
		var count int64
		if err := db.Model(model).Where("policy_id = ?", policy.ID).Count(&count).Error; err != nil {
			t.Fatalf("count retained %s events: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("incomplete delete retained %s events=%d, want 1", name, count)
		}
	}
}

func TestReplicationDeletePartialCleanupRetryFinalizesOnlyAfterAcknowledgement(t *testing.T) {
	db := newClusterHandlerTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationEvent{},
		&clusterModels.ReplicationTransitionEvent{},
	)
	policyID := uint(20)
	otherPolicyID := uint(21)
	policy := clusterModels.ReplicationPolicy{
		ID: policyID, Name: "retry-delete", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 920, SourceNodeID: "node-1", ActiveNodeID: "node-1", OwnerEpoch: 5,
		ProtectionState: clusterModels.ReplicationProtectionStateArmed,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	now := time.Now().UTC()
	localEvents := []clusterModels.ReplicationEvent{
		{ID: 31, PolicyID: &policyID, EventType: "replication", Status: "failed", StartedAt: now},
		{ID: 32, PolicyID: &otherPolicyID, EventType: "replication", Status: "success", StartedAt: now},
		{ID: 33, PolicyID: nil, EventType: "replication", Status: "success", StartedAt: now},
	}
	if err := db.Create(&localEvents).Error; err != nil {
		t.Fatalf("seed local events: %v", err)
	}
	transitionEvents := []clusterModels.ReplicationTransitionEvent{
		{ID: 41, PolicyID: &policyID, TransitionRunID: "delete", EventType: "failover", Status: "failed", StartedAt: now},
		{ID: 42, PolicyID: &otherPolicyID, TransitionRunID: "keep", EventType: "failover", Status: "success", StartedAt: now},
		{ID: 43, PolicyID: nil, TransitionRunID: "unscoped", EventType: "failover", Status: "success", StartedAt: now},
	}
	if err := db.Create(&transitionEvents).Error; err != nil {
		t.Fatalf("seed transition events: %v", err)
	}

	attempts := 0
	cleanup := &replicationPolicyDeleteCleanupStub{cleanup: func(
		_ context.Context,
		gotPolicyID uint,
		minimumRaftAppliedIndex uint64,
	) error {
		attempts++
		if gotPolicyID != policyID || minimumRaftAppliedIndex != 0 {
			t.Fatalf("cleanup authority=(policy %d, index %d), want (%d, 0)", gotPolicyID, minimumRaftAppliedIndex, policyID)
		}
		if attempts == 1 {
			return errors.New("replication_policy_delete_cleanup_partial_failure: node-3 unavailable")
		}
		return nil
	}}
	cS := &cluster.Service{DB: db, NodeID: "node-1"}
	r := newReplicationRouterWithDeleteCleanup(cS, cleanup)

	first := performJSONRequest(t, r, http.MethodDelete, "/cluster/replication/policies/20", nil)
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("first delete response=%d %s", first.Code, first.Body.String())
	}
	var retained clusterModels.ReplicationPolicy
	if err := db.First(&retained, policyID).Error; err != nil {
		t.Fatalf("partial cleanup removed policy: %v", err)
	}
	if retained.ProtectionState != clusterModels.ReplicationProtectionStateDeleting {
		t.Fatalf("partial cleanup lifecycle=%q, want deleting", retained.ProtectionState)
	}
	for name, model := range map[string]any{
		"local":      &clusterModels.ReplicationEvent{},
		"transition": &clusterModels.ReplicationTransitionEvent{},
	} {
		var count int64
		if err := db.Model(model).Where("policy_id = ?", policyID).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("partial cleanup %s event count=%d err=%v, want 1", name, count, err)
		}
	}

	second := performJSONRequest(t, r, http.MethodDelete, "/cluster/replication/policies/20", nil)
	if second.Code != http.StatusOK {
		t.Fatalf("retry delete response=%d %s", second.Code, second.Body.String())
	}
	if attempts != 2 {
		t.Fatalf("cleanup attempts=%d, want 2", attempts)
	}
	var policyCount int64
	if err := db.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", policyID).Count(&policyCount).Error; err != nil || policyCount != 0 {
		t.Fatalf("final policy count=%d err=%v, want 0", policyCount, err)
	}
	for name, model := range map[string]any{
		"local":      &clusterModels.ReplicationEvent{},
		"transition": &clusterModels.ReplicationTransitionEvent{},
	} {
		var exactCount int64
		if err := db.Model(model).Where("policy_id = ?", policyID).Count(&exactCount).Error; err != nil || exactCount != 0 {
			t.Fatalf("final exact %s event count=%d err=%v, want 0", name, exactCount, err)
		}
		var otherCount int64
		if err := db.Model(model).Where("policy_id = ?", otherPolicyID).Count(&otherCount).Error; err != nil || otherCount != 1 {
			t.Fatalf("final other %s event count=%d err=%v, want 1", name, otherCount, err)
		}
		var nullCount int64
		if err := db.Model(model).Where("policy_id IS NULL").Count(&nullCount).Error; err != nil || nullCount != 1 {
			t.Fatalf("final null %s event count=%d err=%v, want 1", name, nullCount, err)
		}
	}
}

func TestReplicationTargetReadinessInternal(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{})
	if err := db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "readiness", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 10,
		ActiveNodeID: "node-1", OwnerEpoch: 7, Enabled: true,
		ProtectionState: clusterModels.ReplicationProtectionStateInitializing,
	}).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if err := db.Create(&clusterModels.ReplicationPolicyTarget{
		PolicyID: 1, NodeID: "node-2", Weight: 100,
	}).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}

	cS := &cluster.Service{DB: db}
	r := newReplicationInternalRouter(cS)
	now := time.Now().UTC().Truncate(time.Millisecond)
	readyUntil := now.Add(time.Hour)
	update := clusterModels.ReplicationTargetReadinessUpdate{
		PolicyID: 1, NodeID: "node-2", ExpectedOwnerEpoch: 7, EvaluatedAt: now,
		Ready: true, GenerationID: "gen-7", ManifestHash: "hash-7",
		RequiredDatasetCount: 2, CompletedDatasetCount: 2,
		LastVerifiedAt: &now, ReadyUntil: &readyUntil,
	}
	body, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal readiness: %v", err)
	}

	rr := performJSONRequest(t, r, http.MethodPost, "/intra/replication-target-readiness", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var target clusterModels.ReplicationPolicyTarget
	if err := db.Where("policy_id = ? AND node_id = ?", 1, "node-2").First(&target).Error; err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !target.Ready || target.OwnerEpoch != 7 || target.GenerationID != "gen-7" {
		t.Fatalf("readiness was not persisted: %+v", target)
	}

	update.ExpectedOwnerEpoch = 6
	update.GenerationID = "stale-generation"
	body, err = json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal stale readiness: %v", err)
	}
	rr = performJSONRequest(t, r, http.MethodPost, "/intra/replication-target-readiness", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 for stale epoch, got %d: %s", rr.Code, rr.Body.String())
	}

	update.ExpectedOwnerEpoch = 7
	update.NodeID = "node-missing"
	update.Ready = false
	update.GenerationID = ""
	update.ManifestHash = ""
	update.RequiredDatasetCount = 0
	update.CompletedDatasetCount = 0
	update.LastVerifiedAt = nil
	update.ReadyUntil = nil
	body, err = json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal missing target readiness: %v", err)
	}
	rr = performJSONRequest(t, r, http.MethodPost, "/intra/replication-target-readiness", body)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing target, got %d: %s", rr.Code, rr.Body.String())
	}

	rr = performJSONRequest(t, r, http.MethodPost, "/intra/replication-target-readiness", []byte(`{}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid update, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestReplicationPoliciesHandlerGet(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{})
	cS := &cluster.Service{DB: db}
	r := newReplicationRouter(cS)

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/replication/policies", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[[]clusterModels.ReplicationPolicy]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Status != "success" || resp.Message != "replication_policies_listed" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected empty, got %d", len(resp.Data))
	}

	policy := clusterModels.ReplicationPolicy{
		ID: 100, Name: "test-policy", GuestType: "vm", GuestID: 1,
		SourceNodeID: "node-1", CronExpr: "*/10 * * * *", Enabled: true,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("failed to seed policy: %v", err)
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/replication/policies", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(resp.Data))
	}
}

func TestReplicationEventsHandlerGet(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ReplicationEvent{})
	cS := &cluster.Service{DB: db}
	r := newReplicationRouter(cS)

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/replication/events", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[[]clusterModels.ReplicationEvent]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected empty, got %d", len(resp.Data))
	}

	policyID := uint(10)
	now := time.Now()
	evt := clusterModels.ReplicationEvent{
		ID: 100, PolicyID: &policyID, EventType: "incremental", Status: "success",
		SourceNodeID: "node-1", TargetNodeID: "node-2",
		StartedAt: now, CompletedAt: &now,
	}
	if err := db.Create(&evt).Error; err != nil {
		t.Fatalf("failed to seed event: %v", err)
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/replication/events?limit=10", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 event, got %d", len(resp.Data))
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/replication/events?policyId=999", nil)
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected 0 with non-existent policy filter, got %d", len(resp.Data))
	}
}

func TestReplicationEventByIDHandler(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ReplicationEvent{})
	cS := &cluster.Service{DB: db}
	r := newReplicationRouter(cS)

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/replication/events/9999", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent, got %d: %s", rr.Code, rr.Body.String())
	}

	policyID := uint(10)
	now := time.Now()
	evt := clusterModels.ReplicationEvent{
		ID: 100, PolicyID: &policyID, EventType: "incremental", Status: "success",
		SourceNodeID: "node-1", TargetNodeID: "node-2",
		StartedAt: now, CompletedAt: &now,
	}
	if err := db.Create(&evt).Error; err != nil {
		t.Fatalf("failed to seed event: %v", err)
	}
	if err := db.Create(&clusterModels.ReplicationTransitionEvent{
		ID: 100, PolicyID: &policyID, TransitionRunID: "transition-100",
		EventType: "failover", Status: "active", StartedAt: now, CompletedAt: &now,
	}).Error; err != nil {
		t.Fatalf("failed to seed transition event: %v", err)
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/replication/events/100", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[*clusterModels.ReplicationEvent]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Message != "replication_event_fetched" {
		t.Fatalf("expected replication_event_fetched, got %q", resp.Message)
	}
	if resp.Data == nil || resp.Data.Scope != clusterModels.ReplicationEventScopeLocal {
		t.Fatalf("unscoped lookup did not preserve local-first compatibility: %+v", resp.Data)
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/replication/events/100?scope=transition", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected transition 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid transition json: %v", err)
	}
	if resp.Data == nil || resp.Data.Scope != clusterModels.ReplicationEventScopeTransition ||
		resp.Data.TransitionRunID != "transition-100" {
		t.Fatalf("scoped transition lookup mismatch: %+v", resp.Data)
	}
}

func newClusterHandlerRouter(cS *cluster.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/cluster", GetCluster(cS))
	return r
}

func TestGetClusterHandler(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ClusterNode{}, &clusterModels.Cluster{})
	cS := &cluster.Service{DB: db}
	r := newClusterHandlerRouter(cS)

	node := clusterModels.ClusterNode{
		NodeUUID: "node-1", Hostname: "node1.local", API: "localhost:8181",
		Status: "online",
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("failed to seed node: %v", err)
	}

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestJoinClusterRejectsInvalidCidr(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/cluster/join", JoinCluster(nil, nil, nil, nil))

	rr := performJSONRequest(t, r, http.MethodPost, "/cluster/join",
		[]byte(`{"nodeId":"n1","nodeIp":"not-a-cidr","nodePort":8181,"clusterKey":"secret","advertiseName":"n1"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestExtractGuestFromDatasetPath(t *testing.T) {
	tests := []struct {
		name     string
		dataset  string
		wantMode string
		wantID   uint
	}{
		{"empty", "", "", 0},
		{"generic", "tank/data/db", "", 0},
		{"jail", "zroot/jails/42", "jail", 42},
		{"vm", "zroot/virtual-machines/7", "vm", 7},
		{"jail subtype", "zroot/jails/42_data", "jail", 42},
		{"vm subtype", "zroot/virtual-machines/7_disk0", "vm", 7},
		{"jail deep", "zroot/jails/deep/42", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, id := extractGuestFromDatasetPath(tt.dataset)
			if mode != tt.wantMode || id != tt.wantID {
				t.Fatalf("extractGuestFromDatasetPath(%q) = (%q, %d), want (%q, %d)",
					tt.dataset, mode, id, tt.wantMode, tt.wantID)
			}
		})
	}
}

func TestCreateReplicationPolicyHandlerValidation(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{})
	cS := &cluster.Service{DB: db}
	r := newReplicationRouter(cS)

	rr := performJSONRequest(t, r, http.MethodPost, "/cluster/replication/policies", []byte(`{}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty payload, got %d: %s", rr.Code, rr.Body.String())
	}

	rr = performJSONRequest(t, r, http.MethodPost, "/cluster/replication/policies",
		[]byte(`{"guestType":"vm","cronExpr":"* * * * *","guestId":1,"failoverMode":"manual","targets":[{"nodeId":"node-2"}]}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing name, got %d: %s", rr.Code, rr.Body.String())
	}

	rr = performJSONRequest(t, r, http.MethodPost, "/cluster/replication/policies",
		[]byte(`{"name":"ab","cronExpr":"* * * * *","guestId":1,"failoverMode":"manual","targets":[{"nodeId":"node-2"}]}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing guestType, got %d: %s", rr.Code, rr.Body.String())
	}

	rr = performJSONRequest(t, r, http.MethodPost, "/cluster/replication/policies",
		[]byte(`{"name":"ab","guestType":"vm","cronExpr":"* * * * *","failoverMode":"manual","targets":[{"nodeId":"node-2"}]}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing guestId, got %d: %s", rr.Code, rr.Body.String())
	}

	rr = performJSONRequest(t, r, http.MethodPost, "/cluster/replication/policies",
		[]byte(`{"name":"ab","guestType":"vm","cronExpr":"* * * * *","guestId":1,"failoverMode":"manual"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing targets, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUpdateReplicationPolicyHandlerValidation(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{})
	cS := &cluster.Service{DB: db}
	r := newReplicationRouter(cS)

	rr := performJSONRequest(t, r, http.MethodPut, "/cluster/replication/policies/1", []byte(`{}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty payload, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestReplicationPolicyHandlerList(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{})
	cS := &cluster.Service{DB: db}
	r := newReplicationRouter(cS)

	policy := clusterModels.ReplicationPolicy{
		Name: "existing-policy", GuestType: "vm", GuestID: 100,
		CronExpr: "@every 1h", FailoverMode: "manual",
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	var listResp handlerAPIResponse[[]clusterModels.ReplicationPolicy]
	listRR := performJSONRequest(t, r, http.MethodGet, "/cluster/replication/policies", nil)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", listRR.Code, listRR.Body.String())
	}
	if err := json.Unmarshal(listRR.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("list unmarshal: %v", err)
	}
	if len(listResp.Data) != 1 {
		t.Fatalf("expected 1 policy in list, got %d", len(listResp.Data))
	}
}

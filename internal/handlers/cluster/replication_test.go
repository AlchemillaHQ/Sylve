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
	"net/http"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

type replicationPolicyDeleteCleanupStub struct {
	cleanup func(context.Context, uint) error
}

func newSingleNodeReplicationHandlerRaft(t *testing.T, fsm *clusterModels.FSMDispatcher) *raft.Raft {
	t.Helper()
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

func (s *replicationPolicyDeleteCleanupStub) CleanupReplicationPolicyDeleteBestEffort(ctx context.Context, policyID uint) error {
	if s == nil || s.cleanup == nil {
		return nil
	}
	return s.cleanup(ctx, policyID)
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
		{
			name:       "policy is not runnable",
			err:        errors.New("replication_policy_not_runnable"),
			wantStatus: http.StatusConflict, wantMessage: "replication_policy_enqueue_failed",
		},
		{
			name:       "unexpected storage failure",
			err:        errors.New("database exploded"),
			wantStatus: http.StatusInternalServerError, wantMessage: "replication_policy_enqueue_failed",
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

	cS := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
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
	cleanup := &replicationPolicyDeleteCleanupStub{cleanup: func(_ context.Context, gotPolicyID uint) error {
		attempts++
		if gotPolicyID != policyID {
			t.Fatalf("cleanup policy=%d, want %d", gotPolicyID, policyID)
		}
		if attempts == 1 {
			return errors.New("replication_policy_delete_cleanup_partial_failure: node-3 unavailable")
		}
		return nil
	}}
	cS := newTestClusterService(db, "node-1")
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

func TestReplicationPoliciesHandlerGet(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{})
	cS := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
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
	cS := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
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

func TestReplicationEventFiltersRejectInvalidValues(t *testing.T) {
	r := newReplicationRouter(nil)
	tests := []string{
		"/cluster/replication/events?limit=invalid&nodeId=remote-node",
		"/cluster/replication/events?limit=0&nodeId=remote-node",
		"/cluster/replication/events?limit=501&nodeId=remote-node",
		"/cluster/replication/events?policyId=invalid&nodeId=remote-node",
		"/cluster/replication/events?policyId=0&nodeId=remote-node",
		"/cluster/replication/events/1?scope=invalid&nodeId=remote-node",
	}

	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			rr := performJSONRequest(t, r, http.MethodGet, path, nil)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
			}

			var response handlerAPIResponse[any]
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Message != "invalid_replication_event_query" {
				t.Fatalf("unexpected response: %+v", response)
			}
		})
	}
}

func TestReplicationEventByIDHandler(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ReplicationEvent{})
	cS := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
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
	cS := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
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
	r.POST("/cluster/join", JoinCluster(nil))

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
	cS := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
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
	cS := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
	r := newReplicationRouter(cS)

	rr := performJSONRequest(t, r, http.MethodPut, "/cluster/replication/policies/1", []byte(`{}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty payload, got %d: %s", rr.Code, rr.Body.String())
	}

	rr = performJSONRequest(t, r, http.MethodPut, "/cluster/replication/policies/1",
		[]byte(`{"name":"ab","guestType":"vm","guestId":1,"targets":[{"nodeId":"node-2"}]}`))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing policy, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestReplicationPolicyJSONBodiesRespectRequestLimit(t *testing.T) {
	cS := &cluster.Service{}
	tests := []struct {
		name    string
		method  string
		path    string
		handler gin.HandlerFunc
	}{
		{
			name: "create", method: http.MethodPost, path: "/cluster/replication/policies",
			handler: CreateReplicationPolicy(cS),
		},
		{
			name: "update", method: http.MethodPut, path: "/cluster/replication/policies/:id",
			handler: UpdateReplicationPolicy(cS, nil),
		},
		{
			name: "failover", method: http.MethodPost, path: "/cluster/replication/policies/:id/failover",
			handler: FailoverReplicationPolicy(cS, &zelta.Service{}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(middleware.LimitRequestBody(64))
			r.Handle(test.method, test.path, test.handler)

			requestPath := strings.Replace(test.path, ":id", "1", 1)
			body := []byte(`{"name":"` + strings.Repeat("x", 128) + `"}`)
			rr := performJSONRequest(t, r, test.method, requestPath, body)
			if rr.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("expected 413, got %d: %s", rr.Code, rr.Body.String())
			}

			var response handlerAPIResponse[any]
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Message != "request_body_too_large" || response.Error != "request_body_too_large" {
				t.Fatalf("unexpected response: %+v", response)
			}
		})
	}
}

func TestReplicationPolicyHandlerList(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{})
	cS := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
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

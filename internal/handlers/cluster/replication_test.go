// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

func newReplicationRouter(cS *cluster.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/cluster/replication/policies", ReplicationPolicies(cS))
	r.POST("/cluster/replication/policies", CreateReplicationPolicy(cS))
	r.PUT("/cluster/replication/policies/:id", UpdateReplicationPolicy(cS, nil))
	r.DELETE("/cluster/replication/policies/:id", DeleteReplicationPolicy(cS, nil))
	r.GET("/cluster/replication/events", ReplicationEvents(cS))
	r.GET("/cluster/replication/events/:id", ReplicationEventByID(cS))
	return r
}

func newReplicationInternalRouter(cS *cluster.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/intra/replication-runtime-state", ReplicationPolicyRuntimeStateInternal(cS, nil))
	r.POST("/intra/activate", ActivateReplicationPolicyInternal(cS, nil))
	r.POST("/intra/demote", DemoteReplicationPolicyInternal(cS, nil))
	r.POST("/intra/catchup", CatchupReplicationPolicyInternal(cS, nil))
	r.POST("/intra/replication-target-readiness", UpdateReplicationTargetReadinessInternal(cS))
	r.POST("/intra/cleanup-policy-delete", CleanupReplicationPolicyDeleteInternal(cS, nil))
	r.POST("/intra/replication-guest-operation-status", ReplicationGuestOperationStatusInternal(cS))
	return r
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

func TestReplicationDeleteDoesNotRemoveMetadataWithoutCleanupService(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{})
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

func TestContainsGuestID(t *testing.T) {
	if containsGuestID([]uint{}, 1) {
		t.Fatal("empty list should not contain")
	}
	if containsGuestID([]uint{1, 2, 3}, 2) == false {
		t.Fatal("should contain 2")
	}
	if containsGuestID([]uint{1, 2, 3}, 4) {
		t.Fatal("should not contain 4")
	}
	if containsGuestID([]uint{1, 2, 3}, 0) {
		t.Fatal("should not contain 0")
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

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
	r.GET("/cluster/replication/receipts", ReplicationReceipts(cS))
	r.POST("/cluster/replication/receipts", UpsertReplicationReceiptInternal(cS))
	return r
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

func TestReplicationReceiptsHandlerGet(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ReplicationReceipt{}, &clusterModels.ReplicationPolicy{})
	cS := &cluster.Service{DB: db}
	r := newReplicationRouter(cS)

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/replication/receipts", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[[]clusterModels.ReplicationReceipt]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected empty, got %d", len(resp.Data))
	}

	now := time.Now()
	receipt := clusterModels.ReplicationReceipt{
		ID: 100, PolicyID: 10, SourceNodeID: "node-1", TargetNodeID: "node-2",
		Status: "success", LastAttemptAt: now,
	}
	if err := db.Create(&receipt).Error; err != nil {
		t.Fatalf("failed to seed receipt: %v", err)
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/replication/receipts?policyId=10", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 receipt, got %d", len(resp.Data))
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/replication/receipts?policyId=0", nil)
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 receipt with policyId=0 (no filter), got %d", len(resp.Data))
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

func TestUpsertReplicationReceiptInternal(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ReplicationReceipt{})
	cS := &cluster.Service{DB: db}
	r := newReplicationRouter(cS)

	rr := performJSONRequest(t, r, http.MethodPost, "/cluster/replication/receipts", []byte(`{}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty payload, got %d: %s", rr.Code, rr.Body.String())
	}

	now := time.Now()
	receiptJSON, _ := json.Marshal(map[string]any{
		"id":            100,
		"policyId":      10,
		"guestType":     "vm",
		"guestId":       1,
		"sourceNodeId":  "node-1",
		"targetNodeId":  "node-2",
		"status":        "success",
		"lastAttemptAt": now.Format(time.RFC3339),
	})
	rr = performJSONRequest(t, r, http.MethodPost, "/cluster/replication/receipts", receiptJSON)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Message != "replication_receipt_upserted" {
		t.Fatalf("expected replication_receipt_upserted, got %q", resp.Message)
	}

	var count int64
	db.Model(&clusterModels.ReplicationReceipt{}).Where("id = 100").Count(&count)
	if count != 1 {
		t.Fatalf("expected receipt persisted, found %d", count)
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

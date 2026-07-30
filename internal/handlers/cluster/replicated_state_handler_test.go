// SPDX-License-Identifier: BSD-2-Clause

package clusterHandlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

func TestReplicatedStateInternalRejectsInvalidAppliedIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/intra-cluster/replicated-state", ReplicatedStateInternal(&cluster.Service{}))

	response := performJSONRequest(
		t,
		router,
		http.MethodGet,
		"/intra-cluster/replicated-state?minimumRaftAppliedIndex=not-a-number",
		nil,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload internal.APIResponse[any]
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Message != "invalid_minimum_raft_applied_index" {
		t.Fatalf("unexpected response: %+v", payload)
	}
}

func TestReplicatedStateRepairInternalValidatesIdentityAndAction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &cluster.Service{NodeID: "node-a"}
	router := gin.New()
	router.POST(
		"/intra-cluster/replicated-state-repair",
		ReplicatedStateRepairInternal(service, nil),
	)

	response := performJSONRequest(
		t,
		router,
		http.MethodPost,
		"/intra-cluster/replicated-state-repair",
		[]byte(`{"action":"fence","expectedNodeId":"node-a"}`),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("fence status=%d body=%s", response.Code, response.Body.String())
	}

	response = performJSONRequest(
		t,
		router,
		http.MethodPost,
		"/intra-cluster/replicated-state-repair",
		[]byte(`{"action":"unfence","expectedNodeId":"node-b"}`),
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("identity mismatch status=%d body=%s", response.Code, response.Body.String())
	}

	response = performJSONRequest(
		t,
		router,
		http.MethodPost,
		"/intra-cluster/replicated-state-repair",
		[]byte(`{"action":"erase-everything","expectedNodeId":"node-a"}`),
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("unsupported action status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestResyncClusterStateHandlerReturnsTypedFailureData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/resync-state", ResyncClusterState(&cluster.Service{}, nil))

	response := performJSONRequest(
		t,
		router,
		http.MethodPost,
		"/cluster/resync-state",
		nil,
	)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload internal.APIResponse[cluster.ClusterStateResyncResult]
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Status != "error" || payload.Message != "error_resyncing_cluster_state" {
		t.Fatalf("unexpected response: %+v", payload)
	}
	if payload.Data.Members == nil {
		t.Fatal("resync failure omitted typed member result list")
	}
}

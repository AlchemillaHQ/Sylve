// SPDX-License-Identifier: BSD-2-Clause

package clusterHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

func TestRemovePeerReturnsStructuredConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/intra-cluster/remove-peer", func(c *gin.Context) {
		writeClusterLeaveError(c, cluster.ClusterLeaveResult{}, &cluster.PeerRemovalBlockedError{
			Conflict: cluster.PeerRemovalConflict{
				NodeID: "node-2",
				Dependencies: []cluster.PeerRemovalDependency{
					{Kind: cluster.PeerRemovalDependencyBackupJob, ID: "17", Name: "daily", Role: "runner"},
				},
			},
		})
	})

	response := performJSONRequest(
		t,
		router,
		http.MethodPost,
		"/intra-cluster/remove-peer",
		[]byte(`{"nodeId":"node-2"}`),
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	var body handlerAPIResponse[cluster.PeerRemovalConflict]
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "error" || body.Message != "peer_removal_blocked" {
		t.Fatalf("response = %#v", body)
	}
	if body.Data.NodeID != "node-2" || len(body.Data.Dependencies) != 1 {
		t.Fatalf("conflict = %#v", body.Data)
	}
	dependency := body.Data.Dependencies[0]
	if dependency.Kind != cluster.PeerRemovalDependencyBackupJob ||
		dependency.ID != "17" || dependency.Name != "daily" || dependency.Role != "runner" {
		t.Fatalf("dependency = %#v", dependency)
	}
}

func TestRemovePeerReturnsNotLeaderConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/intra-cluster/remove-peer", func(c *gin.Context) {
		writeClusterLeaveError(c, cluster.ClusterLeaveResult{}, errors.New("not_leader"))
	})

	response := performJSONRequest(
		t,
		router,
		http.MethodPost,
		"/intra-cluster/remove-peer",
		[]byte(`{"nodeId":"node-2"}`),
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	var body handlerAPIResponse[any]
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "error" || body.Message != "not_leader" {
		t.Fatalf("response = %#v", body)
	}
}

func TestClusterLeaveActiveMutationsReturnsConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/intra-cluster/leave", func(c *gin.Context) {
		writeClusterLeaveError(c, cluster.ClusterLeaveResult{}, &cluster.ClusterLeaveError{
			Code:  "cluster_leave_active_mutations",
			Cause: context.DeadlineExceeded,
		})
	})

	response := performJSONRequest(t, router, http.MethodPost, "/intra-cluster/leave", []byte(`{}`))
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body handlerAPIResponse[cluster.ClusterLeaveResult]
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "error" || body.Message != "cluster_leave_active_mutations" {
		t.Fatalf("response = %#v", body)
	}
}

func TestForceRemovePeerReturnsConsensusUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/remove-node/force", func(c *gin.Context) {
		writeClusterLeaveError(c, cluster.ClusterLeaveResult{}, &cluster.ClusterConsensusError{
			Cause: errors.New("leadership lost"),
		})
	})
	response := performJSONRequest(
		t,
		router,
		http.MethodPost,
		"/cluster/remove-node/force",
		[]byte(`{"nodeId":"node-2","targetExternallyFenced":true}`),
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body handlerAPIResponse[any]
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Message != "cluster_consensus_unavailable" {
		t.Fatalf("response=%#v", body)
	}
}

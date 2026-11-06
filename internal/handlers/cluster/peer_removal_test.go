// SPDX-License-Identifier: BSD-2-Clause

package clusterHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

func TestClusterLeaveErrorResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := newClusterHandlerTestDB(t, &clusterModels.Cluster{})
	if err := database.Create(&clusterModels.Cluster{Enabled: true, LeavePhase: cluster.LeavePhaseRemoving}).Error; err != nil {
		t.Fatal(err)
	}
	service := newTestClusterService(database, "node-1")
	blocked := &cluster.PeerRemovalBlockedError{Conflict: cluster.PeerRemovalConflict{
		NodeID:       "node-2",
		Dependencies: []cluster.PeerRemovalDependency{{Kind: cluster.PeerRemovalDependencyBackupJob, ID: "17", Name: "daily", Role: "runner"}},
	}}
	for _, test := range []struct {
		err         error
		wantStatus  int
		wantMessage string
	}{
		{blocked, http.StatusConflict, "peer_removal_blocked"},
		{&cluster.ClusterLeaveError{Code: "cluster_leave_active_mutations", Cause: context.DeadlineExceeded}, http.StatusConflict, "cluster_leave_active_mutations"},
		{&cluster.ClusterLeaveError{Code: "cluster_leave_membership_unconfirmed", Cause: errors.New("leader unreachable")}, http.StatusAccepted, "cluster_leave_membership_unconfirmed"},
		{cluster.ErrLeaveAlreadyRemoved, http.StatusConflict, "cluster_leave_already_removed"},
		{cluster.ErrPeerNotFound, http.StatusNotFound, "peer_not_found"},
		{cluster.ErrRemoveSelf, http.StatusConflict, "peer_removal_target_is_local"},
		{fmt.Errorf("not_leader: %w", raft.ErrNotLeader), http.StatusConflict, "not_leader"},
		{fmt.Errorf("failed_to_remove_peer: %w", raft.ErrLeadershipLost), http.StatusServiceUnavailable, "cluster_consensus_unavailable"},
		{cluster.ErrNodeLeaveFenced, http.StatusLocked, "node_leave_fenced"},
		{errors.New("disk on fire"), http.StatusInternalServerError, "cluster_leave_failed"},
	} {
		router := gin.New()
		router.POST("/leave", func(c *gin.Context) { writeClusterLeaveError(c, service, test.err) })
		response := performJSONRequest(t, router, http.MethodPost, "/leave", []byte(`{}`))
		var body handlerAPIResponse[json.RawMessage]
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("%v: decode response: %v", test.err, err)
		}
		if response.Code != test.wantStatus || body.Status != "error" || body.Message != test.wantMessage {
			t.Fatalf("%v: status = %d, body = %s", test.err, response.Code, response.Body.String())
		}
	}

	router := gin.New()
	router.POST("/leave", func(c *gin.Context) { writeClusterLeaveError(c, service, blocked) })
	var conflict handlerAPIResponse[cluster.PeerRemovalConflict]
	if err := json.Unmarshal(performJSONRequest(t, router, http.MethodPost, "/leave", nil).Body.Bytes(), &conflict); err != nil ||
		conflict.Data.NodeID != "node-2" || len(conflict.Data.Dependencies) != 1 || conflict.Data.Dependencies[0].Name != "daily" {
		t.Fatalf("conflict = %+v, %v", conflict.Data, err)
	}
}

func TestForceRemovalAndResetRequireAcknowledgement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &cluster.Service{NodeID: "node-1"}
	router := gin.New()
	router.POST("/remove-node/force", ForceRemoveNode(service))
	router.DELETE("/reset-node/force", ForceResetRaftNode(service))
	for _, test := range []struct {
		method, path, body, wantMessage string
	}{
		{http.MethodPost, "/remove-node/force", `{"nodeId":"node-2"}`, "cluster_force_target_fence_ack_required"},
		{http.MethodDelete, "/reset-node/force", `{"nodeId":"node-2","remoteMembershipAcknowledged":true,"workloadsExternallyFenced":true}`, "cluster_force_local_node_id_mismatch"},
		{http.MethodDelete, "/reset-node/force", `{"nodeId":"node-1","workloadsExternallyFenced":true}`, "cluster_force_membership_ack_required"},
		{http.MethodDelete, "/reset-node/force", `{"nodeId":"node-1","remoteMembershipAcknowledged":true}`, "cluster_force_workload_fence_ack_required"},
	} {
		response := performJSONRequest(t, router, test.method, test.path, []byte(test.body))
		var body handlerAPIResponse[any]
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusBadRequest || body.Message != test.wantMessage {
			t.Fatalf("%s %s: status = %d, body = %s", test.method, test.body, response.Code, response.Body.String())
		}
	}
}

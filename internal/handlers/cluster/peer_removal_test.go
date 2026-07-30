// SPDX-License-Identifier: BSD-2-Clause

package clusterHandlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

type peerRemovalServiceStub struct {
	removeErr error
}

func (s peerRemovalServiceStub) RemovePeer(raft.ServerID) error {
	return s.removeErr
}

func (s peerRemovalServiceStub) ClearClusterNode(string) error {
	return nil
}

func TestRemovePeerReturnsStructuredConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/remove-peer", RemovePeer(peerRemovalServiceStub{
		removeErr: &cluster.PeerRemovalBlockedError{
			Conflict: cluster.PeerRemovalConflict{
				NodeID: "node-2",
				Dependencies: []cluster.PeerRemovalDependency{
					{Kind: cluster.PeerRemovalDependencyBackupJob, ID: "17", Name: "daily", Role: "runner"},
				},
			},
		},
	}))

	response := performJSONRequest(
		t,
		router,
		http.MethodPost,
		"/cluster/remove-peer",
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

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package clusterHandlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
)

type replicationRunHandlerStub struct {
	mu       sync.Mutex
	enqueued []uint
}

func (s *replicationRunHandlerStub) EnqueueReplicationPolicyRun(_ context.Context, policyID uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueued = append(s.enqueued, policyID)
	return nil
}

func (s *replicationRunHandlerStub) policyIDs() []uint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uint(nil), s.enqueued...)
}

func newReplicationRunForwardRouter(
	service *clusterService.Service,
	runService replicationPolicyRunService,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("AuthScope", "local")
		c.Set("UserID", uint(7))
		c.Set("Username", "replication-forward-test")
		c.Set("AuthType", "sylve")
		c.Next()
	})
	router.POST("/api/cluster/replication/policies/:id/run", RunReplicationPolicyNow(service, runService))
	return router
}

func performReplicationRunRequest(t *testing.T, router http.Handler, policyID uint) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/cluster/replication/policies/"+strconv.FormatUint(uint64(policyID), 10)+"/run",
		bytes.NewReader([]byte(`{}`)),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestReplicationSyncNowThroughEveryIngressQueuesOnlyOnFollowerOwner(t *testing.T) {
	nodes := setupBackupForwardTestCluster(t, "run-node-a", "run-node-b", "run-node-c")
	leader := waitBackupForwardLeader(t, nodes, 8*time.Second)
	var owner, third *backupForwardTestNode
	for _, node := range nodes {
		if node == leader {
			continue
		}
		if owner == nil {
			owner = node
		} else {
			third = node
		}
	}
	if owner == nil || third == nil {
		t.Fatal("three-node replication forwarding topology was not constructed")
	}
	deadline := time.Now().Add(3 * time.Second)
	for owner.raft.State() != raft.Follower && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if owner.raft.State() != raft.Follower {
		t.Fatalf("active owner state=%s, want follower", owner.raft.State())
	}

	for _, node := range nodes {
		if err := node.service.DB.AutoMigrate(
			&clusterModels.ReplicationPolicy{},
			&clusterModels.ReplicationPolicyTarget{},
		); err != nil {
			t.Fatalf("migrate replication models on %s: %v", node.id, err)
		}
		for _, member := range nodes {
			if err := node.service.DB.Create(&clusterModels.ClusterNode{
				NodeUUID: member.id,
				Hostname: member.id,
				API:      member.id + ":8184",
				Status:   "online",
			}).Error; err != nil {
				t.Fatalf("seed member %s on %s: %v", member.id, node.id, err)
			}
		}
		node.service.AuthService = &backupForwardAuthStub{}
	}

	ownerRun := &replicationRunHandlerStub{}
	leaderRun := &replicationRunHandlerStub{}
	thirdRun := &replicationRunHandlerStub{}
	ownerRouter := newReplicationRunForwardRouter(owner.service, ownerRun)
	routers := map[string]http.Handler{
		leader.id: newReplicationRunForwardRouter(leader.service, leaderRun),
		owner.id:  ownerRouter,
		third.id:  newReplicationRunForwardRouter(third.service, thirdRun),
	}

	originalForward := clusterForwardHTTP
	forwardCalls := 0
	clusterForwardHTTP = func(
		ctx context.Context,
		method string,
		targetURL string,
		body []byte,
		headers map[string]string,
		timeout time.Duration,
	) (clusterForwardResponse, error) {
		forwardCalls++
		if method != http.MethodPost || timeout != clusterForwardDurableTimeout {
			t.Fatalf("forward method=%s timeout=%s", method, timeout)
		}
		parsed, err := url.Parse(targetURL)
		if err != nil {
			return clusterForwardResponse{}, err
		}
		if parsed.Host != owner.id+":8184" {
			t.Fatalf("forward target=%q, want owner %q", parsed.Host, owner.id+":8184")
		}
		request := httptest.NewRequest(method, parsed.Path, bytes.NewReader(body)).WithContext(ctx)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		if request.Header.Get(clusterForwardHopHeader) != "1" {
			t.Fatalf("forward hop=%q, want 1", request.Header.Get(clusterForwardHopHeader))
		}
		response := httptest.NewRecorder()
		ownerRouter.ServeHTTP(response, request)
		return clusterForwardResponse{
			StatusCode: response.Code,
			Header:     response.Header().Clone(),
			Body:       append([]byte(nil), response.Body.Bytes()...),
		}, nil
	}
	t.Cleanup(func() { clusterForwardHTTP = originalForward })

	topologies := []struct {
		name     string
		ingress  *backupForwardTestNode
		policyID uint
	}{
		{name: "leader ingress", ingress: leader, policyID: 8901},
		{name: "owner follower ingress", ingress: owner, policyID: 8902},
		{name: "third follower ingress", ingress: third, policyID: 8903},
	}
	wantOwnerQueue := make([]uint, 0, len(topologies))
	for _, topology := range topologies {
		t.Run(topology.name, func(t *testing.T) {
			policy := clusterModels.ReplicationPolicy{
				ID: topology.policyID, Name: topology.name,
				GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: topology.policyID,
				SourceNodeID: owner.id, ActiveNodeID: owner.id, OwnerEpoch: 2,
				SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
				FailoverMode: clusterModels.ReplicationFailoverManual,
				Enabled:      true, ProtectionState: clusterModels.ReplicationProtectionStateArmed,
				Targets: []clusterModels.ReplicationPolicyTarget{
					{NodeID: leader.id, Weight: 200},
					{NodeID: third.id, Weight: 100},
				},
			}
			for _, node := range nodes {
				nodePolicy := policy
				nodePolicy.Targets = append([]clusterModels.ReplicationPolicyTarget(nil), policy.Targets...)
				if err := clusterModels.UpsertReplicationPolicyTxn(
					node.service.DB, &nodePolicy, nodePolicy.Targets,
				); err != nil {
					t.Fatalf("seed policy on %s: %v", node.id, err)
				}
			}

			response := performReplicationRunRequest(t, routers[topology.ingress.id], topology.policyID)
			if response.Code != http.StatusAccepted ||
				strings.Contains(strings.ToLower(response.Body.String()), "not_leader") {
				t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
			}
			wantOwnerQueue = append(wantOwnerQueue, topology.policyID)
		})
	}

	gotOwnerQueue := ownerRun.policyIDs()
	if len(gotOwnerQueue) != len(wantOwnerQueue) {
		t.Fatalf("owner queue=%v, want %v", gotOwnerQueue, wantOwnerQueue)
	}
	for i := range wantOwnerQueue {
		if gotOwnerQueue[i] != wantOwnerQueue[i] {
			t.Fatalf("owner queue=%v, want %v", gotOwnerQueue, wantOwnerQueue)
		}
	}
	if got := leaderRun.policyIDs(); len(got) != 0 {
		t.Fatalf("leader executed owner work: %v", got)
	}
	if got := thirdRun.policyIDs(); len(got) != 0 {
		t.Fatalf("third follower executed owner work: %v", got)
	}
	if forwardCalls != 2 {
		t.Fatalf("forward calls=%d, want leader and third follower only", forwardCalls)
	}
}

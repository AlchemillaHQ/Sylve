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
	"net"
	"net/http"
	"strconv"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

func newClusterLifecycleValidationRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/cluster", CreateCluster(nil, nil))
	r.POST("/cluster/join", JoinCluster(nil, nil, nil))
	r.POST("/cluster/accept-join", AcceptJoin(nil))
	return r
}

func TestCreateClusterRejectsPayloadWithoutIP(t *testing.T) {
	r := newClusterLifecycleValidationRouter()

	rr := performJSONRequest(t, r, http.MethodPost, "/cluster", []byte(`{"port":8180}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d with body %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if resp.Message != "invalid_request_payload" {
		t.Fatalf("expected invalid_request_payload, got %q", resp.Message)
	}
}

func TestClusterLifecycleRejectsIPv6(t *testing.T) {
	router := newClusterLifecycleValidationRouter()
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "create", path: "/cluster", body: `{"ip":"2001:db8::10"}`},
		{
			name: "join leader", path: "/cluster/join",
			body: `{"nodeId":"node-1","nodeIp":"192.0.2.20","leaderIp":"2001:db8::10","clusterKey":"secret"}`,
		},
		{
			name: "join node", path: "/cluster/join",
			body: `{"nodeId":"node-1","nodeIp":"2001:db8::20","leaderIp":"192.0.2.10","clusterKey":"secret"}`,
		},
		{
			name: "accept join", path: "/cluster/accept-join",
			body: `{"nodeId":"node-1","nodeIp":"2001:db8::20","nodeVersion":"test"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performJSONRequest(t, router, http.MethodPost, test.path, []byte(test.body))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var body handlerAPIResponse[any]
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Message != "cluster_ipv6_unsupported" {
				t.Fatalf("message=%q body=%s", body.Message, response.Body.String())
			}
		})
	}
}

func TestJoinClusterRejectsLegacyLeaderApiPayload(t *testing.T) {
	r := newClusterLifecycleValidationRouter()

	body := []byte(`{"nodeId":"node-1","nodeIp":"10.0.0.2","leaderApi":"10.0.0.1:8184","nodePort":8180,"clusterKey":"secret"}`)
	rr := performJSONRequest(t, r, http.MethodPost, "/cluster/join", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d with body %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if resp.Message != "invalid_request_payload" {
		t.Fatalf("expected invalid_request_payload, got %q", resp.Message)
	}
}

func TestAcceptJoinRejectsPayloadWithoutNodeIP(t *testing.T) {
	r := newClusterLifecycleValidationRouter()

	rr := performJSONRequest(t, r, http.MethodPost, "/cluster/accept-join", []byte(`{"nodeId":"node-1"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d with body %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if resp.Message != "invalid_request_payload" {
		t.Fatalf("expected invalid_request_payload, got %q", resp.Message)
	}
}

func TestAcceptJoinRejectsVersionMismatch(t *testing.T) {
	r := newClusterLifecycleValidationRouter()

	rr := performJSONRequest(
		t,
		r,
		http.MethodPost,
		"/cluster/accept-join",
		[]byte(`{"nodeId":"node-1","nodeIp":"10.0.0.2","nodeVersion":"0.0.0"}`),
	)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d with body %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if resp.Message != "cluster_version_mismatch" {
		t.Fatalf("expected cluster_version_mismatch, got %q", resp.Message)
	}
}

func TestGetJoinKey(t *testing.T) {
	tests := []struct {
		name    string
		cluster clusterModels.Cluster
		want    int
	}{
		{
			name: "enabled cluster", cluster: clusterModels.Cluster{Enabled: true, Key: "cluster-secret"},
			want: http.StatusOK,
		},
		{
			name: "standalone cluster", cluster: clusterModels.Cluster{Enabled: false, Key: "cluster-secret"},
			want: http.StatusConflict,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := newClusterHandlerTestDB(t, &clusterModels.Cluster{})
			if err := db.Create(&test.cluster).Error; err != nil {
				t.Fatalf("create cluster: %v", err)
			}
			router := gin.New()
			router.GET("/cluster/join-key", GetJoinKey(&authService.Service{DB: db}))

			response := performJSONRequest(t, router, http.MethodGet, "/cluster/join-key", nil)
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.want, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" ||
				response.Header().Get("Pragma") != "no-cache" ||
				response.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatalf("missing secret response headers: %v", response.Header())
			}
			if test.want == http.StatusOK {
				var body handlerAPIResponse[JoinKeyResponse]
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if body.Data.Key != "cluster-secret" {
					t.Fatalf("key=%q", body.Data.Key)
				}
			}
		})
	}
}

func TestJoinLeaderAPIHostUsesClusterHTTPSPort(t *testing.T) {
	tests := []struct {
		name string
		ip   string
	}{
		{name: "ipv4", ip: "10.0.0.9"},
		{name: "ipv6", ip: "fd00::9"},
		{name: "trimmed", ip: " 192.168.10.20 "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostPort := joinLeaderAPIHost(tt.ip)
			host, port, err := net.SplitHostPort(hostPort)
			if err != nil {
				t.Fatalf("SplitHostPort failed for %q: %v", hostPort, err)
			}
			if host == "" {
				t.Fatal("expected non-empty host")
			}
			if port != strconv.Itoa(cluster.ClusterEmbeddedHTTPSPort) {
				t.Fatalf("expected cluster HTTPS port %d, got %s", cluster.ClusterEmbeddedHTTPSPort, port)
			}
		})
	}
}

func TestJoinProgressInternalReportsIdentityAndIndexes(t *testing.T) {
	raftNode := setupSingleRaftForTest(t, "node-progress")
	defer func() { _ = raftNode.Shutdown().Error() }()
	service := &cluster.Service{Raft: raftNode, NodeID: "node-progress"}

	router := gin.New()
	router.GET("/intra-cluster/join-progress", JoinProgressInternal(service))
	response := performJSONRequest(
		t,
		router,
		http.MethodGet,
		"/intra-cluster/join-progress?expectedNodeId=node-progress",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body handlerAPIResponse[cluster.ClusterJoinProgress]
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode progress response: %v", err)
	}
	if body.Data.NodeID != "node-progress" || body.Data.AppliedIndex == 0 || body.Data.LastIndex == 0 {
		t.Fatalf("join progress = %+v", body.Data)
	}

	mismatch := performJSONRequest(
		t,
		router,
		http.MethodGet,
		"/intra-cluster/join-progress?expectedNodeId=other-node",
		nil,
	)
	if mismatch.Code != http.StatusConflict {
		t.Fatalf("mismatch status=%d body=%s", mismatch.Code, mismatch.Body.String())
	}
}

func TestGetJoinStatusReturnsDurableIntent(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.Cluster{}, &vmModels.VM{}, &jailModels.Jail{})
	if err := db.Create(&clusterModels.Cluster{RaftPort: cluster.ClusterRaftPort}).Error; err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	service := &cluster.Service{DB: db, NodeID: "joining-node"}
	request := cluster.JoinAdmissionRequest{
		NodeID: "joining-node", NodeIP: "192.0.2.20", NodeVersion: "1.2.3",
		Inventory: cluster.BuildGuestIdentityInventoryReport(nil),
	}
	if err := service.SaveJoinIntent("192.0.2.10", "cluster-key", request); err != nil {
		t.Fatalf("save intent: %v", err)
	}

	router := gin.New()
	router.GET("/cluster/join-status", GetJoinStatus(service))
	response := performJSONRequest(t, router, http.MethodGet, "/cluster/join-status", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body handlerAPIResponse[cluster.ClusterJoinStatus]
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if body.Data.NodeID != "joining-node" || body.Data.Phase != cluster.JoinPhaseIntentSaved ||
		!body.Data.Retrying {
		t.Fatalf("join status = %+v", body.Data)
	}
}

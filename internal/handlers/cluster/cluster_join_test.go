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

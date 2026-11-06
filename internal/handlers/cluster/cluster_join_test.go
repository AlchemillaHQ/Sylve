// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/crypto"
	"github.com/gin-gonic/gin"
)

func newClusterLifecycleValidationRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/cluster", CreateCluster(nil))
	r.POST("/cluster/join", JoinCluster(nil))
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
			body: `{"nodeId":"node-1","nodeIp":"2001:db8::20","nodeVersion":"test","nodeHostname":"node-1","sshPublicKey":"ssh-ed25519 AAAA"}`,
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
	service := newTestClusterService(newClusterHandlerTestDB(t, &clusterModels.Cluster{}), "leader-node")
	if err := service.ReopenMutations(); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/cluster/accept-join", AcceptJoin(service))

	rr := performAcceptJoin(t, r, `{"nodeId":"node-1","nodeIp":"10.0.0.2","nodeVersion":"0.0.0","nodeHostname":"node-1","sshPublicKey":"ssh-ed25519 AAAA"}`, true)
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

func TestGenerateJoinKey(t *testing.T) {
	service, cleanup := setupHandlerRaftCluster(t)
	defer cleanup()
	cert := newJoinTestCert(t)
	if err := service.SetClusterCert(cert); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Create(&clusterModels.Cluster{RaftIP: "192.0.2.10"}).Error; err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/join-key", GenerateJoinKey(service))
	generate := func(want int) cluster.JoinKey {
		t.Helper()
		response := performJSONRequest(t, router, http.MethodPost, "/cluster/join-key", nil)
		if response.Code != want {
			t.Fatalf("status=%d want=%d body=%s", response.Code, want, response.Body.String())
		}
		if response.Header().Get("Cache-Control") != "no-store" ||
			response.Header().Get("Pragma") != "no-cache" ||
			response.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("missing secret response headers: %v", response.Header())
		}
		var body handlerAPIResponse[JoinKeyResponse]
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if want != http.StatusOK {
			return cluster.JoinKey{}
		}
		key, err := cluster.ParseJoinKey(body.Data.Key)
		if err != nil || key.Pin != cluster.CertPin(cert.Leaf) || body.Data.Address != "192.0.2.10" {
			t.Fatalf("key %q (%v) at %q", body.Data.Key, err, body.Data.Address)
		}
		return key
	}

	generate(http.StatusConflict)
	if err := service.DB.Model(&clusterModels.Cluster{}).Where("id = ?", 1).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	first := generate(http.StatusOK)
	if !clusterModels.JoinKeyMatches(service.DB, first.Secret) {
		t.Fatal("the generated key doesn't admit")
	}
	second := generate(http.StatusOK)
	if clusterModels.JoinKeyMatches(service.DB, first.Secret) || !clusterModels.JoinKeyMatches(service.DB, second.Secret) {
		t.Fatal("a new key didn't replace the last")
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

func TestGetJoinStatusReturnsDurableIntent(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.Cluster{}, &vmModels.VM{}, &jailModels.Jail{})
	if err := db.Create(&clusterModels.Cluster{
		Key: "cluster-key", RaftIP: "192.0.2.20", RaftPort: cluster.ClusterRaftPort, JoinLeaderIP: "192.0.2.10",
	}).Error; err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	service := newTestClusterService(db, "joining-node")

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
	if body.Data.NodeID != "joining-node" || body.Data.Phase != cluster.JoinPhaseSubmitting || !body.Data.Retrying ||
		body.Data.LeaderIP != "192.0.2.10" || body.Data.NodeIP != "192.0.2.20" {
		t.Fatalf("join status = %+v", body.Data)
	}
}

func newJoinTestCert(t *testing.T) tls.Certificate {
	t.Helper()
	certPEM, keyPEM, err := crypto.GenerateClusterCertificate()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func performAcceptJoin(t *testing.T, r http.Handler, body string, withCert bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/cluster/accept-join", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if withCert {
		request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{newJoinTestCert(t).Leaf}}
	}
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	return response
}

func TestAcceptJoinRequiresAClientCert(t *testing.T) {
	service := newTestClusterService(newClusterHandlerTestDB(t, &clusterModels.Cluster{}), "leader-node")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/cluster/accept-join", AcceptJoin(service))

	rr := performAcceptJoin(t, r, `{"nodeId":"node-1","nodeIp":"10.0.0.2","nodeVersion":"0.0.0","nodeHostname":"node-1","sshPublicKey":"ssh-ed25519 AAAA"}`, false)
	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusConflict || resp.Error != "joining_node_cert_required" {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMembershipStatusNeedsTheNodesCert(t *testing.T) {
	service, cleanup := setupHandlerRaftCluster(t)
	defer cleanup()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cluster/membership-status", MembershipStatus(service))
	ask := func(nodeID string, cert *x509.Certificate) (int, handlerAPIResponse[cluster.MembershipStatus]) {
		t.Helper()
		body, _ := json.Marshal(MembershipStatusRequest{NodeID: nodeID})
		request := httptest.NewRequest(http.MethodPost, "/cluster/membership-status", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		if cert != nil {
			request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		var decoded handlerAPIResponse[cluster.MembershipStatus]
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		return response.Code, decoded
	}

	stranger := newJoinTestCert(t).Leaf
	if code, body := ask("gone-node", nil); code != http.StatusUnauthorized || body.Message != "cluster_node_cert_required" {
		t.Fatalf("without a cert: %d %+v", code, body)
	}
	if code, body := ask(service.LocalNodeID(), stranger); code != http.StatusForbidden || body.Message != "membership_status_cert_mismatch" {
		t.Fatalf("a member's status by another cert: %d %+v", code, body)
	}
	if code, body := ask("gone-node", stranger); code != http.StatusOK || body.Data.Present {
		t.Fatalf("a non-member's status: %d %+v", code, body)
	}
}

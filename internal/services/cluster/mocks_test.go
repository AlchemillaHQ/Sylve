// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
)

// clusterAuthStub provides a minimal AuthService stub for cluster tests.
// Embed the real interface so only overridden methods need implementations.
type clusterAuthStub struct {
	serviceInterfaces.AuthServiceInterface
}

func (clusterAuthStub) CreateInternalClusterJWT(_, _ string) (string, error) {
	return "test-cluster-token", nil
}

func (clusterAuthStub) CreateClusterJWT(_ uint, _, _, _ string) (string, error) {
	return "test-cluster-token", nil
}

// clusterPeerSimulator stands up an httptest TLS server that simulates a
// cluster peer node's intra-cluster API. Register handlers for specific
// endpoints before starting the test.
type clusterPeerSimulator struct {
	server    *httptest.Server
	addr      string // host:port of the test server
	serveMux  *http.ServeMux
	requests  []clusterPeerRequest // captured requests in order
}

type clusterPeerRequest struct {
	Method string
	Path   string
	Body   string
	Header http.Header
}

func newClusterPeerSimulator() *clusterPeerSimulator {
	mux := http.NewServeMux()
	sim := &clusterPeerSimulator{serveMux: mux}
	sim.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			r.Body.Read(bodyBytes)
		}
		sim.requests = append(sim.requests, clusterPeerRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   string(bodyBytes),
			Header: r.Header,
		})
		mux.ServeHTTP(w, r)
	}))
	sim.addr = strings.TrimPrefix(sim.server.URL, "https://")
	return sim
}

func (sim *clusterPeerSimulator) Close() {
	sim.server.Close()
}

func (sim *clusterPeerSimulator) Addr() string {
	return sim.addr
}

// NumRequests returns the count of captured HTTP requests.
func (sim *clusterPeerSimulator) NumRequests() int {
	return len(sim.requests)
}

// FindRequest returns the first captured request where the path matches, or nil.
func (sim *clusterPeerSimulator) FindRequest(path string) *clusterPeerRequest {
	for i := range sim.requests {
		if sim.requests[i].Path == path {
			return &sim.requests[i]
		}
	}
	return nil
}

// HandleSyncHealth registers a handler that responds to intra-cluster sync-health POSTs.
func (sim *clusterPeerSimulator) HandleSyncHealth(respond200 bool) {
	sim.serveMux.HandleFunc("/api/intra-cluster/sync-health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if respond200 {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success"}`))
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	})
}

// HandleEncryptionKeyDiscover registers a handler for the encryption key discovery endpoint.
func (sim *clusterPeerSimulator) HandleEncryptionKeyDiscover(respond200 bool) {
	sim.serveMux.HandleFunc("/api/intra-cluster/encryption-key/discover", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if respond200 {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success"}`))
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	})
}

// HandlePanelRefresh registers a handler for the left-panel-refresh fanout endpoint.
func (sim *clusterPeerSimulator) HandlePanelRefresh() {
	sim.serveMux.HandleFunc("/api/intra-cluster/events/left-panel-refresh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))
	})
}

// HandleNodeInfo registers a handler that returns cluster node info.
func (sim *clusterPeerSimulator) HandleNodeInfo(nodeUUID, hostname string, healthOK bool) {
	sim.serveMux.HandleFunc("/api/info/node", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		info := map[string]any{
			"hostname": hostname,
		}
		resp := map[string]any{
			"status": "success",
			"data":   info,
		}
		respJSON, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(respJSON)
	})
}

// HandleHealthHTTP registers a handler that returns a health check response.
func (sim *clusterPeerSimulator) HandleHealthHTTP(healthy bool) {
	sim.serveMux.HandleFunc("/api/health/http", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if healthy {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
}

// seedPeerNodeAPIs writes ClusterNode rows on the leader so that the service
// can resolve the httptest peer addresses.
func seedPeerNodeAPIs(leader *clusterRaftTestNode, peerNodes ...*clusterRaftTestNode) {
	for _, n := range peerNodes {
		leader.service.DB.Create(&clusterModels.ClusterNode{
			NodeUUID: n.id,
			Hostname: n.id,
			API:      n.id + ":8184", // will be overridden by tests that need real URLs
			Status:   "online",
		})
	}
}

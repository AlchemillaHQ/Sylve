// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func useRoutingTestHostname(t *testing.T, value string) {
	t.Helper()

	previous := hostname
	hostname = value
	t.Cleanup(func() {
		hostname = previous
	})
}

func encodedRoutingAuth(t *testing.T, selectedHostname string) string {
	t.Helper()

	payload, err := json.Marshal(map[string]string{
		"hash":     "test-token-hash",
		"hostname": selectedHostname,
	})
	if err != nil {
		t.Fatalf("encode routing auth: %v", err)
	}

	return hex.EncodeToString(payload)
}

func newSelectedNodeTestRouter(database *gorm.DB, localCalls *int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(EnsureCorrectHost(database, nil))
	router.GET("/resource", func(c *gin.Context) {
		(*localCalls)++
		c.Status(http.StatusNoContent)
	})
	return router
}

func assertSelectedNodeError(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantStatus int,
	wantCode string,
) {
	t.Helper()

	if response.Code != wantStatus {
		t.Fatalf("status=%d want=%d body=%s", response.Code, wantStatus, response.Body.String())
	}

	var body internal.APIResponse[any]
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "error" || body.Message != wantCode || body.Error != wantCode {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestEnsureCorrectHostAllowsLocalRequests(t *testing.T) {
	useRoutingTestHostname(t, "origin-node")
	localAuth := encodedRoutingAuth(t, "origin-node")

	tests := []struct {
		name    string
		path    string
		headers http.Header
	}{
		{
			name: "no selected node",
			path: "/resource",
		},
		{
			name: "explicit local node",
			path: "/resource",
			headers: http.Header{
				"X-Current-Hostname": []string{"origin-node"},
			},
		},
		{
			name: "local node in auth query",
			path: "/resource?auth=" + url.QueryEscape(localAuth),
		},
		{
			name: "bearer websocket authentication without node selection",
			path: "/resource",
			headers: http.Header{
				"Sec-Websocket-Protocol": []string{"Bearer, browser-token"},
			},
		},
		{
			name: "bearer websocket authentication with auth-query selection",
			path: "/resource?auth=" + url.QueryEscape(localAuth),
			headers: http.Header{
				"Sec-Websocket-Protocol": []string{"Bearer, browser-token"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			localCalls := 0
			router := newSelectedNodeTestRouter(nil, &localCalls)
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			request.Header = tt.headers.Clone()
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != http.StatusNoContent || localCalls != 1 {
				t.Fatalf(
					"status=%d localCalls=%d body=%s",
					response.Code,
					localCalls,
					response.Body.String(),
				)
			}
		})
	}
}

func TestEnsureCorrectHostRejectsMalformedExplicitSelection(t *testing.T) {
	useRoutingTestHostname(t, "origin-node")

	tests := []struct {
		name    string
		path    string
		headers http.Header
	}{
		{
			name: "empty hostname header",
			path: "/resource",
			headers: http.Header{
				"X-Current-Hostname": []string{""},
			},
		},
		{
			name: "whitespace hostname header",
			path: "/resource",
			headers: http.Header{
				"X-Current-Hostname": []string{"   "},
			},
		},
		{
			name: "malformed auth query",
			path: "/resource?auth=not-hex",
		},
		{
			name: "malformed routing websocket protocol",
			path: "/resource",
			headers: http.Header{
				"Sec-Websocket-Protocol": []string{"not-hex"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			localCalls := 0
			router := newSelectedNodeTestRouter(nil, &localCalls)
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			request.Header = tt.headers.Clone()
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			assertSelectedNodeError(t, response, http.StatusBadRequest, "invalid_selected_node")
			if localCalls != 0 {
				t.Fatalf("local handler called %d times", localCalls)
			}
		})
	}
}

func TestEnsureCorrectHostRejectsUnknownOfflineAndUnavailableNodes(t *testing.T) {
	useRoutingTestHostname(t, "origin-node")
	database := testutil.NewSQLiteTestDB(t, &clusterModels.ClusterNode{})
	for _, node := range []clusterModels.ClusterNode{
		{
			NodeUUID: "offline-node-id",
			Hostname: "offline-node",
			Status:   "offline",
			API:      "offline-node:8181",
		},
		{
			NodeUUID: "unavailable-node-id",
			Hostname: "unavailable-node",
			Status:   "online",
			API:      "",
		},
	} {
		if err := database.Create(&node).Error; err != nil {
			t.Fatalf("seed cluster node: %v", err)
		}
	}

	tests := []struct {
		name       string
		hostname   string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unknown node",
			hostname:   "unknown-node",
			wantStatus: http.StatusNotFound,
			wantCode:   "selected_node_not_found",
		},
		{
			name:       "offline node",
			hostname:   "offline-node",
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "selected_node_offline",
		},
		{
			name:       "online node without API address",
			hostname:   "unavailable-node",
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "selected_node_unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			localCalls := 0
			router := newSelectedNodeTestRouter(database, &localCalls)
			request := httptest.NewRequest(http.MethodGet, "/resource", nil)
			request.Header.Set("X-Current-Hostname", tt.hostname)
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			assertSelectedNodeError(t, response, tt.wantStatus, tt.wantCode)
			if localCalls != 0 {
				t.Fatalf("local handler called %d times", localCalls)
			}
		})
	}
}

func TestEnsureCorrectHostRejectsUnavailableNodeLookup(t *testing.T) {
	useRoutingTestHostname(t, "origin-node")
	localCalls := 0
	router := newSelectedNodeTestRouter(nil, &localCalls)
	request := httptest.NewRequest(http.MethodGet, "/resource", nil)
	request.Header.Set("X-Current-Hostname", "remote-node")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assertSelectedNodeError(t, response, http.StatusInternalServerError, "selected_node_lookup_failed")
	if localCalls != 0 {
		t.Fatalf("local handler called %d times", localCalls)
	}
}

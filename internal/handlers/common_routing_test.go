// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func useRoutingTestHostname(t *testing.T, value string) {
	t.Helper()

	previous := hostname
	hostname = value
	t.Cleanup(func() {
		hostname = previous
	})
}

func remoteMember(host string) func(string) (string, error) {
	return func(node string) (string, error) {
		if node == "remote-node" || node == "remote-node-id" {
			return host, nil
		}
		return "", cluster.ErrNodeNotFound
	}
}

func newOriginRouter(t *testing.T, members func(string) (string, error), scope string) (*gin.Engine, models.User) {
	t.Helper()

	database := testutil.NewSQLiteTestDB(t, &models.User{})
	user := models.User{Username: "admin", Admin: true}
	if err := database.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	useRoutingTestHostname(t, "origin-node")

	gin.SetMode(gin.TestMode)
	router := gin.New()
	signedIn := func(c *gin.Context) {
		c.Set("Token", "browser-local-token")
		c.Set("AuthScope", scope)
		c.Set("UserID", user.ID)
		c.Set("Username", user.Username)
		c.Set("AuthType", "sylve")
		c.Next()
	}
	cS := cluster.NewClusterService(database, &authService.Service{DB: database}, nil).(*cluster.Service)
	registerNodeRoute(router.Group("/api"), cS, members, signedIn)
	return router, user
}

func assertNodeRouteError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
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

func TestNodePathsServeThisNodeHere(t *testing.T) {
	nodeID, err := utils.GetSystemUUID()
	if err != nil {
		t.Fatal(err)
	}
	router, _ := newOriginRouter(t, remoteMember(""), "local")
	var served string
	router.GET("/api/vm/:rid", func(c *gin.Context) {
		served = c.Request.URL.Path
		c.Status(http.StatusNoContent)
	})
	handler := NodePaths(router)

	for _, path := range []string{"/api/nodes/" + nodeID + "/vm/7", "/api/nodes/origin-node/vm/7", "/api/vm/7"} {
		served = ""
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNoContent || served != "/api/vm/7" {
			t.Fatalf("%s: status=%d served=%q", path, response.Code, served)
		}
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/nodes/elsewhere/vm/7", nil))
	assertNodeRouteError(t, response, http.StatusNotFound, "node_not_found")
}

func TestNodeRouteAuthenticatesThePlainPath(t *testing.T) {
	var seen string
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerNodeRoute(router.Group("/api"), nil, remoteMember(""), func(c *gin.Context) {
		seen = c.Request.URL.Path
		c.AbortWithStatus(http.StatusUnauthorized)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/nodes/remote-node/vm/7/console?auth=00", nil))
	if response.Code != http.StatusUnauthorized || seen != "/api/vm/7/console" {
		t.Fatalf("status=%d authenticated %q", response.Code, seen)
	}
}

func TestNodeRouteRefusesWhatItCantForward(t *testing.T) {
	failing := func(err error) func(string) (string, error) {
		return func(string) (string, error) { return "", err }
	}
	tests := []struct {
		name    string
		node    string
		members func(string) (string, error)
		scope   string
		status  int
		code    string
	}{
		{"unknown node", "unknown-node", remoteMember(""), "local", http.StatusNotFound, "node_not_found"},
		{"shared hostname", "remote-node", failing(cluster.ErrNodeNameAmbiguous), "local", http.StatusConflict, "node_name_ambiguous"},
		{"no configuration", "remote-node", failing(errors.New("get_config_failed")), "local", http.StatusServiceUnavailable, "node_unavailable"},
		{"a member's request", "remote-node", remoteMember("127.0.0.1:1"), "cluster", http.StatusMisdirectedRequest, "node_forward_second_hop"},
		{"unreachable node", "remote-node", remoteMember("127.0.0.1:1"), "local", http.StatusBadGateway, "node_unreachable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router, _ := newOriginRouter(t, test.members, test.scope)
			response := newCloseNotifyRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/nodes/"+test.node+"/vm/7", nil))
			assertNodeRouteError(t, response.ResponseRecorder, test.status, test.code)
		})
	}
}

func TestNodeRouteLeavesCompressionToTheNode(t *testing.T) {
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !strings.Contains(request.Header.Get("Accept-Encoding"), "gzip") {
			_, _ = w.Write([]byte("plain"))
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		writer := gzip.NewWriter(w)
		_, _ = writer.Write([]byte("compressed by the node"))
		_ = writer.Close()
	}))
	defer remote.Close()
	router, _ := newOriginRouter(t, remoteMember(strings.TrimPrefix(remote.URL, "https://")), "local")

	request := httptest.NewRequest(http.MethodGet, "/api/nodes/remote-node/vm/7", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := newCloseNotifyRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("status=%d Content-Encoding=%q", response.Code, response.Header().Get("Content-Encoding"))
	}
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if body, err := io.ReadAll(reader); err != nil || string(body) != "compressed by the node" {
		t.Fatalf("body=%q, %v", body, err)
	}
}

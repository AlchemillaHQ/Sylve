// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
	closed chan bool
}

func newCloseNotifyRecorder() *closeNotifyRecorder {
	return &closeNotifyRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		closed:           make(chan bool),
	}
}

func (r *closeNotifyRecorder) CloseNotify() <-chan bool {
	return r.closed
}

func TestPublicSignedDownloadRoutesOnlyToKnownNodeAndStripsCredentials(t *testing.T) {
	var forwardedAuthorization string
	var forwardedClusterToken string
	var forwardedCookie string
	var forwardedNode string
	var forwardedRange string
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		forwardedAuthorization = request.Header.Get("Authorization")
		forwardedClusterToken = request.Header.Get("X-Cluster-Token")
		forwardedCookie = request.Header.Get("Cookie")
		forwardedNode = request.URL.Query().Get("node")
		forwardedRange = request.Header.Get("Range")
		w.Header().Set("Content-Disposition", `attachment; filename="remote.iso"`)
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("remote"))
	}))
	defer remote.Close()

	database := testutil.NewSQLiteTestDB(t, &clusterModels.ClusterNode{})
	if err := database.Create(&clusterModels.ClusterNode{
		NodeUUID: "remote-node-id",
		Status:   "online",
		Hostname: "remote-node",
		API:      strings.TrimPrefix(remote.URL, "https://"),
	}).Error; err != nil {
		t.Fatal(err)
	}

	previousHostname := hostname
	hostname = "origin-node"
	t.Cleanup(func() { hostname = previousHostname })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	downloadUUID := utils.GenerateRandomUUID()
	router.GET(
		"/api/utilities/downloads/:uuid",
		EnsurePublicDownloadHost(database),
		func(c *gin.Context) { c.String(http.StatusTeapot, "origin") },
	)
	query := url.Values{
		"expires": []string{"1893456000"},
		"id":      []string{"42"},
		"node":    []string{"remote-node"},
		"sig":     []string{"capability"},
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/utilities/downloads/"+downloadUUID+"?"+query.Encode(),
		nil,
	)
	request.Header.Set("Authorization", "Bearer browser-token")
	request.Header.Set("Cookie", "session=browser")
	request.Header.Set("Range", "bytes=0-5")
	request.Header.Set("X-Cluster-Token", "Bearer cluster-token")
	response := newCloseNotifyRecorder()
	router.ServeHTTP(response, request)

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusPartialContent || string(body) != "remote" {
		t.Fatalf("status=%d body=%q", response.Code, body)
	}
	if forwardedAuthorization != "" || forwardedClusterToken != "" || forwardedCookie != "" {
		t.Fatalf(
			"forwarded credentials: authorization=%q cluster=%q cookie=%q",
			forwardedAuthorization,
			forwardedClusterToken,
			forwardedCookie,
		)
	}
	if forwardedNode != "remote-node" || forwardedRange != "bytes=0-5" {
		t.Fatalf("node=%q range=%q", forwardedNode, forwardedRange)
	}
}

func TestPublicSignedDownloadStopsRoutingAtLocalNode(t *testing.T) {
	previousHostname := hostname
	hostname = "local-node"
	t.Cleanup(func() { hostname = previousHostname })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET(
		"/api/utilities/downloads/:uuid",
		EnsurePublicDownloadHost(nil),
		func(c *gin.Context) { c.String(http.StatusTeapot, "local") },
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(
		http.MethodGet,
		"/api/utilities/downloads/"+utils.GenerateRandomUUID()+"?node=local-node",
		nil,
	))

	if response.Code != http.StatusTeapot || response.Body.String() != "local" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

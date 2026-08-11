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
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestSelectedNodeDownloadStripsOriginCredentialsAndInjectsClusterCredential(t *testing.T) {
	type downloadAuth struct {
		Hash     string `json:"hash"`
		Hostname string `json:"hostname"`
		Token    string `json:"token,omitempty"`
	}

	var forwardedClusterHeader string
	var forwardedID string
	var forwardedHash string
	var forwardedAuth string
	var forwardedHeaders http.Header
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		forwardedHeaders = request.Header.Clone()
		forwardedClusterHeader = request.Header.Get("X-Cluster-Token")
		forwardedID = request.URL.Query().Get("id")
		forwardedHash = request.URL.Query().Get("hash")
		forwardedAuth = request.URL.Query().Get("auth")

		w.Header().Set("Content-Disposition", `attachment; filename="remote.iso"`)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("remote file contents"))
	}))
	defer remote.Close()

	database := testutil.NewSQLiteTestDB(
		t,
		&models.User{},
		&clusterModels.Cluster{},
		&clusterModels.ClusterNode{},
	)
	if err := database.Create(&clusterModels.Cluster{Enabled: true, Key: "cluster-secret"}).Error; err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	user := models.User{Username: "admin", Admin: true}
	if err := database.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := database.Create(&clusterModels.ClusterNode{
		NodeUUID: "remote-node-id",
		Status:   "online",
		Hostname: "remote-node",
		API:      strings.TrimPrefix(remote.URL, "https://"),
	}).Error; err != nil {
		t.Fatalf("seed remote node: %v", err)
	}

	previousHostname := hostname
	hostname = "origin-node"
	t.Cleanup(func() {
		hostname = previousHostname
	})

	service := &authService.Service{DB: database}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("Token", "browser-local-token")
		c.Set("AuthScope", "local")
		c.Set("UserID", user.ID)
		c.Set("Username", user.Username)
		c.Set("AuthType", "sylve")
		c.Next()
	})
	router.Use(EnsureCorrectHost(database, service))
	router.GET("/api/system/file-explorer/download", func(c *gin.Context) {
		c.String(http.StatusTeapot, "download reached origin handler")
	})

	initialAuth, err := json.Marshal(downloadAuth{
		Hash:     "browser-token-hash",
		Hostname: "remote-node",
	})
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{
		"id":   []string{"/zroot/images/remote.iso"},
		"hash": []string{"browser-token-hash"},
		"auth": []string{hex.EncodeToString(initialAuth)},
	}
	origin := httptest.NewServer(router)
	defer origin.Close()
	request, err := http.NewRequest(
		http.MethodGet,
		origin.URL+"/api/system/file-explorer/download?"+query.Encode(),
		nil,
	)
	if err != nil {
		t.Fatalf("create origin request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer browser-local-token")
	request.Header.Set("Cookie", "session=browser")
	request.Header.Set("Proxy-Authorization", "Basic browser")
	request.Header.Set("X-Cluster-Key", "must-not-forward")
	request.Header.Set("X-Current-Hostname", "remote-node")
	request.Header.Set("X-Sylve-Cluster-Forward-Hop", "99")
	request.Header.Set("X-Sylve-Backup-Forwarded-By", "browser")
	request.Header.Set("X-Sylve-Backup-Forward-Target", "browser")
	response, err := origin.Client().Do(request)
	if err != nil {
		t.Fatalf("perform origin request: %v", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read origin response: %v", err)
	}

	if response.StatusCode != http.StatusOK || string(responseBody) != "remote file contents" {
		t.Fatalf("response=%d body=%q", response.StatusCode, responseBody)
	}
	if got := response.Header.Get("Content-Disposition"); got != `attachment; filename="remote.iso"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	if forwardedID != "/zroot/images/remote.iso" {
		t.Fatalf("forwarded id=%q hash=%q", forwardedID, forwardedHash)
	}
	if forwardedHash != "" || forwardedAuth != "" {
		t.Fatalf("origin query credentials leaked: hash=%q auth=%q", forwardedHash, forwardedAuth)
	}
	if !strings.HasPrefix(forwardedClusterHeader, "Bearer ") || strings.TrimPrefix(forwardedClusterHeader, "Bearer ") == "" {
		t.Fatalf("cluster credential was not injected: %q", forwardedClusterHeader)
	}
	for _, name := range []string{
		"Authorization", "Cookie", "Proxy-Authorization", "X-Cluster-Key", "X-Current-Hostname",
		"X-Sylve-Backup-Forwarded-By", "X-Sylve-Backup-Forward-Target",
	} {
		if got := forwardedHeaders.Get(name); got != "" {
			t.Fatalf("origin header %s leaked: %q", name, got)
		}
	}
	if got := forwardedHeaders.Get("X-Sylve-Cluster-Forward-Hop"); got != "" {
		t.Fatalf("browser forward-hop header leaked: %q", got)
	}
}

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

func TestSelectedNodeDownloadUsesEncodedHostnameAndInjectsClusterCredential(t *testing.T) {
	type downloadAuth struct {
		Hash     string `json:"hash"`
		Hostname string `json:"hostname"`
		Token    string `json:"token,omitempty"`
	}

	var forwardedAuth downloadAuth
	var forwardedClusterHeader string
	var forwardedID string
	var forwardedHash string
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		forwardedClusterHeader = request.Header.Get("X-Cluster-Token")
		forwardedID = request.URL.Query().Get("id")
		forwardedHash = request.URL.Query().Get("hash")

		authBytes, err := hex.DecodeString(request.URL.Query().Get("auth"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(authBytes, &forwardedAuth); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

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
	if err := database.Create(&clusterModels.Cluster{Key: "cluster-secret"}).Error; err != nil {
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
	if forwardedID != "/zroot/images/remote.iso" || forwardedHash != "browser-token-hash" {
		t.Fatalf("forwarded id=%q hash=%q", forwardedID, forwardedHash)
	}
	if forwardedAuth.Hash != "browser-token-hash" || forwardedAuth.Hostname != "remote-node" {
		t.Fatalf("forwarded auth = %+v", forwardedAuth)
	}
	if forwardedAuth.Token == "" {
		t.Fatal("forwarded auth did not receive a cluster token")
	}
	if forwardedClusterHeader != "Bearer "+forwardedAuth.Token {
		t.Fatalf("cluster header does not match injected auth token")
	}
}

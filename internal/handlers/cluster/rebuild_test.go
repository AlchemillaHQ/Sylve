// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

func TestRebuildHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/cluster/rebuild", Rebuild(cluster.NewClusterService(newClusterHandlerTestDB(t), nil, nil).(*cluster.Service)))
	body := []byte(`{"nodeId":"node-a"}`)

	plain := httptest.NewRecorder()
	router.ServeHTTP(plain, httptest.NewRequest(http.MethodPost, "/api/cluster/rebuild", bytes.NewReader(body)))
	if plain.Code != http.StatusBadRequest {
		t.Fatalf("without TLS: %d %s", plain.Code, plain.Body.String())
	}

	server := httptest.NewTLSServer(router)
	defer server.Close()
	response, err := server.Client().Post(server.URL+"/api/cluster/rebuild", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("not rebuilding: %d", response.StatusCode)
	}
}

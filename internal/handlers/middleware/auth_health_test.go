// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import (
	"net/http"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	authSvc "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestHealthClusterKeyAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)

	database := testutil.NewSQLiteTestDB(t, &clusterModels.Cluster{})
	if err := database.Create(&clusterModels.Cluster{Enabled: true, Key: "cluster-secret"}).Error; err != nil {
		t.Fatalf("failed to seed cluster key: %v", err)
	}

	authService := &authSvc.Service{DB: database}
	router := gin.New()
	router.Use(AuthenticateBasicHealth(authService))
	router.GET("/api/health/basic", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	tests := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{
			name:    "valid key",
			headers: map[string]string{authSvc.ClusterKeyHeader: "cluster-secret"},
			want:    http.StatusOK,
		},
		{
			name:    "invalid key",
			headers: map[string]string{authSvc.ClusterKeyHeader: "invalid"},
			want:    http.StatusUnauthorized,
		},
		{
			name: "malformed cluster token overrides valid key",
			headers: map[string]string{
				authSvc.ClusterTokenHeader: "not-bearer",
				authSvc.ClusterKeyHeader:   "cluster-secret",
			},
			want: http.StatusUnauthorized,
		},
		{name: "missing credentials", want: http.StatusUnauthorized},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := testutil.PerformRequest(
				t,
				router,
				http.MethodGet,
				"/api/health/basic",
				nil,
				test.headers,
			)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

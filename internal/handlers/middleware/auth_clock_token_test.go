// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	authSvc "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
)

func TestClusterTokenClockErrorsAreDistinct(t *testing.T) {
	gin.SetMode(gin.TestMode)

	database := testutil.NewSQLiteTestDB(t, &clusterModels.Cluster{})
	if err := database.Create(&clusterModels.Cluster{Enabled: true, Key: "cluster-secret"}).Error; err != nil {
		t.Fatalf("seed cluster key: %v", err)
	}
	authService := &authSvc.Service{DB: database}

	sign := func(issuedAt, expiresAt time.Time) string {
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, authSvc.JWT{
			RegisteredClaims: jwt.RegisteredClaims{
				IssuedAt:  jwt.NewNumericDate(issuedAt),
				ExpiresAt: jwt.NewNumericDate(expiresAt),
			},
			CustomClaims: serviceInterfaces.CustomClaims{
				TokenUse: authSvc.ClusterTokenUseInternalControl,
			},
		}).SignedString([]byte("cluster-secret"))
		if err != nil {
			t.Fatalf("sign token: %v", err)
		}
		return token
	}

	now := time.Now()
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{name: "expired", token: sign(now.Add(-2*time.Minute), now.Add(-time.Minute)), want: "cluster_token_expired"},
		{name: "future issued", token: sign(now.Add(2*time.Minute), now.Add(3*time.Minute)), want: "cluster_token_future_issued"},
		{name: "invalid", token: "not-a-jwt", want: "invalid_cluster_token"},
	}

	router := gin.New()
	router.Use(EnsureAuthenticated(authService))
	router.GET("/api/intra-cluster/join-progress", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := testutil.PerformRequest(
				t,
				router,
				http.MethodGet,
				"/api/intra-cluster/join-progress",
				nil,
				map[string]string{authSvc.ClusterTokenHeader: "Bearer " + test.token},
			)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusUnauthorized, response.Body.String())
			}

			var body internal.APIResponse[any]
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Message != test.want {
				t.Fatalf("message = %q, want %q", body.Message, test.want)
			}
		})
	}
}

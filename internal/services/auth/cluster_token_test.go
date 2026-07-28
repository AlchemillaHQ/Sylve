// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestUserProxyClusterJWTIncludesAdminClaim(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.User{}, &clusterModels.Cluster{})
	if err := db.Create(&clusterModels.Cluster{Key: "cluster-secret"}).Error; err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	user := models.User{Username: "admin", Admin: true}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	service := &Service{DB: db}

	token, err := service.CreateClusterJWT(user.ID, "  submitted-name  ", "sylve", "")
	if err != nil {
		t.Fatalf("create cluster token: %v", err)
	}
	claims, err := service.VerifyClusterJWT(token)
	if err != nil {
		t.Fatalf("verify cluster token: %v", err)
	}
	if !claims.Admin || claims.TokenUse != ClusterTokenUseUserProxy || claims.UserID != user.ID || claims.Username != user.Username {
		t.Fatalf("unexpected claims: %#v", claims)
	}
}

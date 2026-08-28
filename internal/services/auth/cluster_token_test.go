// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestUserProxyClusterJWTIncludesAdminClaim(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.User{}, &clusterModels.Cluster{})
	if err := db.Create(&clusterModels.Cluster{Enabled: true, Key: "cluster-secret"}).Error; err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	user := models.User{Username: "admin", Admin: true}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	service := &Service{DB: db}
	if err := service.SetClusterIssuerNodeID("node-1"); err != nil {
		t.Fatal(err)
	}

	token, err := service.CreateUserProxyJWT(user.ID, "  submitted-name  ", "sylve")
	if err != nil {
		t.Fatalf("create cluster token: %v", err)
	}
	claims, err := service.VerifyClusterJWT(token)
	if err != nil {
		t.Fatalf("verify cluster token: %v", err)
	}
	if !claims.Admin || claims.TokenUse != ClusterTokenUseUserProxy || claims.UserID != user.ID ||
		claims.Username != user.Username || claims.IssuerNodeID != "node-1" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
}

func TestAuthorizeClusterIssuerMembershipMatrix(t *testing.T) {
	service := &Service{}
	service.SetClusterIssuerVerifier(func(nodeID string) (ClusterIssuerMembership, error) {
		switch nodeID {
		case "voter":
			return ClusterIssuerMembership{Suffrage: "voter"}, nil
		case "nonvoter":
			return ClusterIssuerMembership{Suffrage: "nonvoter"}, nil
		case "staging":
			return ClusterIssuerMembership{Suffrage: "staging"}, nil
		default:
			return ClusterIssuerMembership{}, fmt.Errorf("not_current")
		}
	})

	tests := []struct {
		name    string
		claims  serviceInterfaces.CustomClaims
		method  string
		path    string
		wantErr bool
	}{
		{name: "issuer required", claims: serviceInterfaces.CustomClaims{TokenUse: ClusterTokenUseInternalControl}, method: http.MethodPost, path: "/api/intra-cluster/sync", wantErr: true},
		{name: "absent issuer", claims: serviceInterfaces.CustomClaims{IssuerNodeID: "absent", TokenUse: ClusterTokenUseInternalControl}, method: http.MethodPost, path: "/api/intra-cluster/sync", wantErr: true},
		{name: "admin proxy voter", claims: serviceInterfaces.CustomClaims{IssuerNodeID: "voter", TokenUse: ClusterTokenUseUserProxy, UserID: 1, Admin: true}, method: http.MethodGet, path: "/api/vm"},
		{name: "admin proxy nonvoter", claims: serviceInterfaces.CustomClaims{IssuerNodeID: "nonvoter", TokenUse: ClusterTokenUseUserProxy, UserID: 1, Admin: true}, method: http.MethodGet, path: "/api/vm", wantErr: true},
		{name: "service read nonvoter", claims: serviceInterfaces.CustomClaims{IssuerNodeID: "nonvoter", TokenUse: ClusterTokenUseUserProxy}, method: http.MethodGet, path: "/api/info/node"},
		{name: "internal voter", claims: serviceInterfaces.CustomClaims{IssuerNodeID: "voter", TokenUse: ClusterTokenUseInternalControl}, method: http.MethodPost, path: "/api/intra-cluster/sync"},
		{name: "nonvoter identity", claims: serviceInterfaces.CustomClaims{IssuerNodeID: "nonvoter", TokenUse: ClusterTokenUseInternalControl}, method: http.MethodPost, path: "/api/intra-cluster/ssh-identity"},
		{name: "staging remove", claims: serviceInterfaces.CustomClaims{IssuerNodeID: "staging", TokenUse: ClusterTokenUseInternalControl}, method: http.MethodPost, path: "/api/intra-cluster/remove-peer"},
		{name: "nonvoter other", claims: serviceInterfaces.CustomClaims{IssuerNodeID: "nonvoter", TokenUse: ClusterTokenUseInternalControl}, method: http.MethodPost, path: "/api/intra-cluster/sync", wantErr: true},
		{name: "nonvoter wrong method", claims: serviceInterfaces.CustomClaims{IssuerNodeID: "nonvoter", TokenUse: ClusterTokenUseInternalControl}, method: http.MethodGet, path: "/api/intra-cluster/ssh-identity", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := service.AuthorizeClusterIssuer(test.claims, test.method, test.path)
			if (err != nil) != test.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, test.wantErr)
			}
		})
	}
}

func TestClusterJWTCannotUseEmptyConfiguredKey(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &clusterModels.Cluster{})
	if err := db.Create(&clusterModels.Cluster{Enabled: true, Key: "   "}).Error; err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	service := &Service{DB: db}
	if err := service.SetClusterIssuerNodeID("node-1"); err != nil {
		t.Fatal(err)
	}

	if _, err := service.CreateUserProxyJWT(0, "admin", "sylve"); err == nil ||
		!strings.Contains(err.Error(), "cluster_key_not_configured") {
		t.Fatalf("expected cluster_key_not_configured, got: %v", err)
	}
}

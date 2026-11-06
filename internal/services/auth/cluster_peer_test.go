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
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestProxiedUserIsAnUnlockedAdmin(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.User{})
	users := map[string]*models.User{
		"admin":    {Username: "admin", Admin: true},
		"plain":    {Username: "plain"},
		"locked":   {Username: "locked", Admin: true, Locked: true},
		"passless": {Username: "passless", Admin: true, DisablePassword: true},
	}
	for _, user := range users {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{DB: db}
	tests := []struct {
		user, authType string
		ok             bool
	}{
		{"admin", "sylve", true},
		{"admin", AuthTypeSylvePasskey, true},
		{"admin", "token", false},
		{"plain", "sylve", false},
		{"locked", "sylve", false},
		{"passless", "sylve", false},
		{"passless", AuthTypeSylvePasskey, true},
	}
	for _, test := range tests {
		user := users[test.user]
		got, err := service.ProxiedUser(user.ID, test.authType)
		if (err == nil) != test.ok {
			t.Fatalf("%s/%s: error %v, want ok=%t", test.user, test.authType, err, test.ok)
		}
		if test.ok && got != (ProxiedUser{Username: user.Username, AuthType: test.authType}) {
			t.Fatalf("%s/%s: proxied %+v", test.user, test.authType, got)
		}
	}
	if _, err := service.ProxiedUser(9999, "sylve"); err == nil {
		t.Fatal("an unknown user was proxied")
	}
}

func TestParseProxiedUserRoundTripsAndRejectsJunk(t *testing.T) {
	user := ProxiedUser{Username: "ädmin", AuthType: "pam"}
	if got, err := ParseProxiedUser(user.Header()); err != nil || got != user {
		t.Fatalf("round trip: %+v, %v", got, err)
	}
	for _, value := range []string{
		"", "not json", `{"authType":"sylve"}`, `{"username":" ","authType":"sylve"}`,
		`{"username":"a","authType":"internal-cluster"}`,
	} {
		if _, err := ParseProxiedUser(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}

func TestAuthorizeClusterPeerMembershipMatrix(t *testing.T) {
	service := &Service{}
	service.SetClusterIssuerVerifier(func(nodeID string) (ClusterIssuerMembership, error) {
		switch nodeID {
		case "voter", "nonvoter", "staging":
			return ClusterIssuerMembership{Suffrage: nodeID}, nil
		default:
			return ClusterIssuerMembership{}, fmt.Errorf("not_current")
		}
	})

	tests := []struct {
		name, node, caller, method, path string
		wantErr                          bool
	}{
		{"absent member", "absent", ClusterCallerNode, http.MethodPost, "/api/intra-cluster/node/x", true},
		{"admin through voter", "voter", ClusterCallerUser, http.MethodGet, "/api/vm", false},
		{"admin through nonvoter", "nonvoter", ClusterCallerUser, http.MethodGet, "/api/vm", true},
		{"service read nonvoter", "nonvoter", ClusterCallerNode, http.MethodGet, "/api/info/node", false},
		{"call from voter", "voter", ClusterCallerNode, http.MethodPost, "/api/intra-cluster/node/x", false},
		{"nonvoter removes itself", "nonvoter", ClusterCallerNode, http.MethodPost, "/api/intra-cluster/leader/remove-peer", false},
		{"staging removes itself", "staging", ClusterCallerNode, http.MethodPost, "/api/intra-cluster/leader/remove-peer", false},
		{"nonvoter proposal", "nonvoter", ClusterCallerNode, http.MethodPost, "/api/intra-cluster/raft/apply", true},
		{"nonvoter other call", "nonvoter", ClusterCallerNode, http.MethodPost, "/api/intra-cluster/node/x", true},
		{"nonvoter removal by GET", "nonvoter", ClusterCallerNode, http.MethodGet, "/api/intra-cluster/leader/remove-peer", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := service.AuthorizeClusterPeer(test.node, test.caller, test.method, test.path)
			if (err != nil) != test.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, test.wantErr)
			}
		})
	}
}

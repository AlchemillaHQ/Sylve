// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	authSvc "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/crypto"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func newPeerCert(t *testing.T) *x509.Certificate {
	t.Helper()
	certPEM, keyPEM, err := crypto.GenerateClusterCertificate()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert.Leaf
}

func TestEnsureAuthenticatedTrustBoundary(t *testing.T) {
	member, stranger := newPeerCert(t), newPeerCert(t)
	pin := cluster.CertPin(member)
	database := testutil.NewSQLiteTestDB(t,
		&models.User{}, &models.Token{}, &models.SystemSecrets{}, &clusterModels.ClusterSSHIdentity{},
	)
	if err := database.Create(&models.SystemSecrets{Name: "JWTSecret", Data: "local-secret"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&clusterModels.ClusterSSHIdentity{
		NodeUUID: "node-1", SSHHost: "10.0.0.1", PublicKey: "ssh-ed25519 AAAA", CertPin: hex.EncodeToString(pin[:]),
	}).Error; err != nil {
		t.Fatal(err)
	}
	const passwordHash = "$2a$04$q8RMvfpZU3MpIHropi1cfu0QobOdx.lw1SwiSLPC2ctgMMtPw6cc."
	user := models.User{Username: "admin", Password: passwordHash, Admin: true}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	service := authSvc.NewAuthService(database).(*authSvc.Service)
	service.SetClusterIssuerVerifier(func(nodeID string) (authSvc.ClusterIssuerMembership, error) {
		if nodeID != "node-1" {
			return authSvc.ClusterIssuerMembership{}, fmt.Errorf("not_current")
		}
		return authSvc.ClusterIssuerMembership{Suffrage: "voter"}, nil
	})
	peers := cluster.NewClusterService(database, nil, nil).(*cluster.Service)
	_, localToken, err := service.CreateJWT(user.Username, "correct horse battery staple", "sylve", false)
	if err != nil {
		t.Fatal(err)
	}
	localHash := utils.SHA256(localToken, 1)
	consoleAuth, _ := json.Marshal(map[string]string{"hash": localHash, "hostname": "origin"})
	consoleAuthHex := hex.EncodeToString(consoleAuth)
	admin := authSvc.ProxiedUser{Username: user.Username, AuthType: "sylve"}.Header()
	realmUser := authSvc.ProxiedUser{Username: "alice", AuthType: authSvc.AuthTypeOIDC, Realm: "company"}.Header()
	bearer := "Bearer " + localToken

	tests := []struct {
		name    string
		cert    *x509.Certificate
		method  string
		path    string
		headers map[string]string
		want    int
	}{
		{"local bearer", nil, http.MethodGet, "/api/vm", map[string]string{"Authorization": bearer}, http.StatusOK},
		{"console hash", nil, http.MethodGet, "/api/vm/1/console?auth=" + consoleAuthHex, nil, http.StatusOK},
		{"member call", member, http.MethodPost, "/api/intra-cluster/node/x", nil, http.StatusOK},
		{"member at the intra prefix", member, http.MethodPost, "/api/intra-cluster/", nil, http.StatusForbidden},
		{"member service read", member, http.MethodGet, "/api/info/node", nil, http.StatusOK},
		{"member version probe", member, http.MethodGet, "/api/health/basic", nil, http.StatusOK},
		{"member service read near miss", member, http.MethodGet, "/api/info/node/extra", nil, http.StatusForbidden},
		{"member container tree", member, http.MethodGet, "/api/container/simple", nil, http.StatusOK},
		{"member full container list", member, http.MethodGet, "/api/container", nil, http.StatusForbidden},
		{"member ordinary route ignores a bearer", member, http.MethodGet, "/api/vm", map[string]string{"Authorization": bearer}, http.StatusForbidden},
		{"admin through member", member, http.MethodGet, "/api/vm", map[string]string{authSvc.ProxyUserHeader: admin}, http.StatusOK},
		{"admin through member at a call", member, http.MethodPost, "/api/intra-cluster/node/x", map[string]string{authSvc.ProxyUserHeader: admin}, http.StatusForbidden},
		{"malformed proxy user", member, http.MethodGet, "/api/vm", map[string]string{authSvc.ProxyUserHeader: "{"}, http.StatusUnauthorized},
		{"stranger acting for an admin", stranger, http.MethodGet, "/api/vm", map[string]string{authSvc.ProxyUserHeader: admin}, http.StatusUnauthorized},
		{"stranger call", stranger, http.MethodPost, "/api/intra-cluster/node/x", nil, http.StatusUnauthorized},
		{"stranger with a session", stranger, http.MethodGet, "/api/vm", map[string]string{"Authorization": bearer}, http.StatusOK},
		{"no cert acting for an admin", nil, http.MethodGet, "/api/vm", map[string]string{authSvc.ProxyUserHeader: admin}, http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(EnsureAuthenticated(service))
			router.Handle(test.method, testRoute(test.path), func(c *gin.Context) { c.Status(http.StatusOK) })
			request := httptest.NewRequest(test.method, test.path, nil)
			for key, value := range test.headers {
				request.Header.Set(key, value)
			}
			if test.cert != nil {
				request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{test.cert}}
			}
			response := httptest.NewRecorder()
			peers.PeerHandler(router).ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.want, response.Body.String())
			}
		})
	}

	router := gin.New()
	router.Use(EnsureAuthenticated(service))
	router.GET("/api/vm", func(c *gin.Context) { c.Status(http.StatusOK) })
	request := httptest.NewRequest(http.MethodGet, "/api/vm", nil)
	request.Header.Add(authSvc.ProxyUserHeader, admin)
	request.Header.Add(authSvc.ProxyUserHeader, admin)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{member}}
	response := httptest.NewRecorder()
	peers.PeerHandler(router).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("repeated proxy header status=%d", response.Code)
	}

	for _, test := range []struct {
		name    string
		cert    *x509.Certificate
		path    string
		headers map[string]string
		want    string
	}{
		{"local bearer", nil, "/api/vm", map[string]string{"Authorization": bearer}, fmt.Sprintf("user:%d id|admin", user.ID)},
		{"console hash", nil, "/api/vm/1/console?auth=" + consoleAuthHex, nil, fmt.Sprintf("user:%d id|admin", user.ID)},
		{"admin through member", member, "/api/vm", map[string]string{authSvc.ProxyUserHeader: admin}, "node:node-1:sylve:admin|admin"},
		{"realm user through member", member, "/api/vm", map[string]string{authSvc.ProxyUserHeader: realmUser},
			"node:node-1:oidc:alice@company|alice@company"},
		{"member service read", member, "/api/info/node", nil, "|node-1"},
	} {
		router := gin.New()
		router.Use(EnsureAuthenticated(service))
		router.GET(testRoute(test.path), func(c *gin.Context) {
			owner := c.GetString("Owner")
			if _, ok := c.Get("UserID"); ok {
				owner += " id"
			}
			c.String(http.StatusOK, owner+"|"+c.GetString("Username"))
		})
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		for key, value := range test.headers {
			request.Header.Set(key, value)
		}
		if test.cert != nil {
			request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{test.cert}}
		}
		response := httptest.NewRecorder()
		peers.PeerHandler(router).ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != test.want {
			t.Fatalf("%s: status=%d owner %q, want %q", test.name, response.Code, response.Body.String(), test.want)
		}
	}
}

func TestAuthenticateJoin(t *testing.T) {
	member, stranger := newPeerCert(t), newPeerCert(t)
	pin := cluster.CertPin(member)
	database := testutil.NewSQLiteTestDB(t, &clusterModels.ClusterSSHIdentity{}, &clusterModels.ClusterOption{})
	if err := database.Create(&clusterModels.ClusterSSHIdentity{
		NodeUUID: "node-1", SSHHost: "10.0.0.1", PublicKey: "ssh-ed25519 AAAA", CertPin: hex.EncodeToString(pin[:]),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&clusterModels.ClusterOption{ID: 1, JoinKeyHash: clusterModels.JoinKeyHash("join-secret")}).Error; err != nil {
		t.Fatal(err)
	}
	service := &authSvc.Service{DB: database}
	peers := cluster.NewClusterService(database, nil, nil).(*cluster.Service)

	tests := []struct {
		name string
		cert *x509.Certificate
		key  string
		want int
	}{
		{"joiner with the key", stranger, "join-secret", http.StatusOK},
		{"joiner with another key", stranger, "spent-secret", http.StatusUnauthorized},
		{"joiner without a key", stranger, "", http.StatusUnauthorized},
		{"member forwarding a key", member, "spent-secret", http.StatusOK},
		{"member without a key", member, "", http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var seen string
			router := gin.New()
			router.POST("/api/cluster/accept-join", AuthenticateJoin(service), func(c *gin.Context) {
				seen = c.GetString("ClusterKey")
				c.Status(http.StatusOK)
			})
			request := httptest.NewRequest(http.MethodPost, "/api/cluster/accept-join", nil)
			if test.key != "" {
				request.Header.Set(authSvc.ClusterKeyHeader, test.key)
			}
			request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{test.cert}}
			response := httptest.NewRecorder()
			peers.PeerHandler(router).ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.want, response.Body.String())
			}
			if test.want == http.StatusOK && seen != test.key {
				t.Fatalf("the handler sees key %q, want %q", seen, test.key)
			}
		})
	}
}

func testRoute(path string) string {
	path = strings.SplitN(path, "?", 2)[0]
	return strings.Replace(path, "/api/vm/1/", "/api/vm/:rid/", 1)
}

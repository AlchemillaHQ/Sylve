// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package authHandlers

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/gin-gonic/gin"
)

func newOIDCHandlerTest(t *testing.T) (*authService.Service, *gin.Engine) {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": server.URL, "authorization_endpoint": server.URL + "/authorize",
			"token_endpoint": server.URL + "/token", "jwks_uri": server.URL + "/jwks",
		})
	}))
	t.Cleanup(server.Close)

	svc := newTestAuthService(t)
	if err := svc.InitSecret("JWTSecret"); err != nil {
		t.Fatal(err)
	}
	secret := "s3cret"
	if err := svc.SetRealm(context.Background(), clusterModels.AuthRealmSet{
		AuthRealm: clusterModels.AuthRealm{
			Name: "company", Issuer: server.URL, ClientID: "sylve", Scopes: "openid", UsernameClaim: "preferred_username",
			SessionHours: 8, Enabled: true,
			CACert: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})),
		},
		ClientSecret: &secret,
	}); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/api/auth/oidc/:realm/start", StartOIDCLoginHandler(svc))
	router.POST("/api/auth/oidc/callback", OIDCCallbackHandler(svc))
	router.GET("/api/auth/realms", ListRealmsHandler(svc))
	return svc, router
}

func serveJSON(router *gin.Engine, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestOIDCStartBindsTheBrowser(t *testing.T) {
	_, router := newOIDCHandlerTest(t)
	rec := serveJSON(router, http.MethodPost, "/api/auth/oidc/company/start", `{"redirectUri":"https://node.example/oidc/callback"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/authorize?") {
		t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authService.OIDCStateCookie || !cookies[0].HttpOnly || !cookies[0].Secure ||
		cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/api/auth/oidc" || cookies[0].Value == "" {
		t.Fatalf("cookies = %+v", cookies)
	}
	var started struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	authURL, err := url.Parse(started.Data.URL)
	if err != nil {
		t.Fatal(err)
	}
	state := authURL.Query().Get("state")
	if state == "" || strings.Contains(cookies[0].Value, state) {
		t.Fatalf("state %q, cookie %q: the cookie must carry the login state sealed", state, cookies[0].Value)
	}

	for name, cookie := range map[string]*http.Cookie{"without the cookie": nil, "with another state": cookies[0]} {
		body := `{"code":"c","state":"` + state + `"}`
		if cookie != nil {
			body = `{"code":"c","state":"some-other-state"}`
		}
		var extra []*http.Cookie
		if cookie != nil {
			extra = append(extra, cookie)
		}
		rec = serveJSON(router, http.MethodPost, "/api/auth/oidc/callback", body, extra...)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "oidc_login_expired") {
			t.Fatalf("callback %s = %d %s", name, rec.Code, rec.Body.String())
		}
	}

	rec = serveJSON(router, http.MethodPost, "/api/auth/oidc/unknown/start", `{"redirectUri":"https://node.example/oidc/callback"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unknown realm = %d %s", rec.Code, rec.Body.String())
	}
}

func TestListRealmsHidesTheSecret(t *testing.T) {
	_, router := newOIDCHandlerTest(t)
	rec := serveJSON(router, http.MethodGet, "/api/auth/realms", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hasSecret":true`) || strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("realms = %d %s", rec.Code, rec.Body.String())
	}
}

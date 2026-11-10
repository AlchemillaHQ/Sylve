// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/golang-jwt/jwt/v4"
)

const (
	testClientID     = "sylve"
	testClientSecret = "s3cret"
	testRedirectURI  = "https://node.example" + OIDCCallbackPath
)

type fakeIdP struct {
	server        *httptest.Server
	key           *rsa.PrivateKey
	forger        *rsa.PrivateKey
	mu            sync.Mutex
	codes         map[string]fakeCode
	userInfo      map[string]map[string]any
	userInfoCalls int
	forge         bool
}

type fakeCode struct {
	challenge   string
	redirectURI string
	idClaims    map[string]any
	infoClaims  map[string]any
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	forger, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeIdP{key: key, forger: forger, codes: map[string]fakeCode{}, userInfo: map[string]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{
			"issuer": p.server.URL, "authorization_endpoint": p.server.URL + "/authorize",
			"token_endpoint": p.server.URL + "/token", "userinfo_endpoint": p.server.URL + "/userinfo",
			"jwks_uri": p.server.URL + "/jwks", "response_types_supported": []string{"code"},
			"subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"},
			"token_endpoint_auth_methods_supported": []string{"client_secret_basic"},
			"scopes_supported":                      []string{"openid", "profile", "email", "groups"},
			"claims_supported":                      []string{"sub", "preferred_username", "groups"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		id, secret, _ := r.BasicAuth()
		if err := r.ParseForm(); err != nil || id != testClientID || secret != testClientSecret {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		p.mu.Lock()
		code, ok := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		p.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != code.challenge || r.Form.Get("redirect_uri") != code.redirectURI {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		signer := p.key
		if p.forge {
			signer = p.forger
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims(code.idClaims))
		token.Header["kid"] = "k1"
		idToken, err := token.SignedString(signer)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		access := rand.Text()
		p.mu.Lock()
		p.userInfo[access] = code.infoClaims
		p.mu.Unlock()
		writeTestJSON(w, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 300, "id_token": idToken})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.userInfoCalls++
		claims, ok := p.userInfo[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		p.mu.Unlock()
		if !ok {
			http.Error(w, "unknown token", http.StatusUnauthorized)
			return
		}
		writeTestJSON(w, claims)
	})
	p.server = httptest.NewTLSServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (p *fakeIdP) caCert() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: p.server.Certificate().Raw}))
}

// authorize plays the browser at the provider's login page and returns the state and code.
func (p *fakeIdP) authorize(t *testing.T, authURL string, claims, info map[string]any) (string, string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	now := time.Now().Unix()
	idClaims := map[string]any{"iss": p.server.URL, "aud": testClientID, "iat": now, "exp": now + 300, "nonce": query.Get("nonce")}
	maps.Copy(idClaims, claims)
	infoClaims := map[string]any{"sub": idClaims["sub"]}
	maps.Copy(infoClaims, info)
	code := rand.Text()
	p.mu.Lock()
	p.codes[code] = fakeCode{
		challenge: query.Get("code_challenge"), redirectURI: query.Get("redirect_uri"), idClaims: idClaims, infoClaims: infoClaims,
	}
	p.mu.Unlock()
	return query.Get("state"), code
}

type oidcHarness struct {
	svc   *Service
	idp   *fakeIdP
	realm clusterModels.AuthRealm
}

func newOIDCHarness(t *testing.T, configure func(*clusterModels.AuthRealm)) *oidcHarness {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t,
		&models.User{}, &models.Group{}, &models.Token{}, &models.SystemSecrets{}, &models.Migrations{},
		&models.WebAuthnCredential{}, &models.WebAuthnChallenge{}, &clusterModels.ACLGrant{}, &clusterModels.ACLRole{},
		&clusterModels.ACLGroup{}, &clusterModels.SylveUser{}, &clusterModels.AuthRealm{}, &clusterModels.OIDCUser{},
	)
	seedRows := []any{
		&models.SystemSecrets{Name: "JWTSecret", Data: "oidc-test-signing-secret"},
		&models.Migrations{Name: adminGrantsMigration},
	}
	for _, row := range seedRows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := newAuthService(db, fakePasswordHasher{})
	useFSMProposer(t, svc)
	h := &oidcHarness{svc: svc, idp: newFakeIdP(t)}
	h.realm = clusterModels.AuthRealm{
		Name: "company", Issuer: h.idp.server.URL, ClientID: testClientID, Scopes: "openid profile email groups",
		UsernameClaim: "preferred_username", GroupsClaim: "groups", QueryUserInfo: true, SessionHours: 8,
		CACert: h.idp.caCert(), Enabled: true,
	}
	if configure != nil {
		configure(&h.realm)
	}
	secret := testClientSecret
	h.setRealm(t, &secret)
	return h
}

func (h *oidcHarness) setRealm(t *testing.T, secret *string) {
	t.Helper()
	if err := h.svc.SetRealm(context.Background(), clusterModels.AuthRealmSet{AuthRealm: h.realm, ClientSecret: secret}); err != nil {
		t.Fatalf("set realm: %v", err)
	}
}

func (h *oidcHarness) login(t *testing.T, claims, info map[string]any) (uint, string, error) {
	t.Helper()
	authURL, cookie, err := h.svc.StartOIDCLogin(context.Background(), h.realm.Name, testRedirectURI)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	returned, code := h.idp.authorize(t, authURL, claims, info)
	return h.svc.FinishOIDCLogin(context.Background(), returned, cookie, code)
}

func (h *oidcHarness) group(t *testing.T, name, role string, members ...string) {
	t.Helper()
	ctx := context.Background()
	if err := h.svc.SetACLGroup(ctx, name, members); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetGrant(ctx, clusterModels.ACLGrant{Path: "/", SubjectType: clusterModels.ACLSubjectGroup, Subject: name, Role: role, Propagate: true}); err != nil {
		t.Fatal(err)
	}
}

func (h *oidcHarness) user(t *testing.T, name string) models.User {
	t.Helper()
	var user models.User
	if err := h.svc.DB.Where("username = ?", name).First(&user).Error; err != nil {
		t.Fatalf("user %s: %v", name, err)
	}
	return user
}

func (h *oidcHarness) hasAccess(t *testing.T, name string) bool {
	t.Helper()
	user := h.user(t, name)
	allowed, err := h.svc.HasAccess(PrincipalOf(&user))
	if err != nil {
		t.Fatal(err)
	}
	return allowed
}

func wantLoginError(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || err.Error() != code {
		t.Fatalf("login error = %v, want %s", err, code)
	}
}

func TestOIDCLoginIssuesARealmSession(t *testing.T) {
	h := newOIDCHarness(t, func(realm *clusterModels.AuthRealm) {
		realm.AutoCreate, realm.Prompt = true, "login"
	})
	h.group(t, "ops", "Auditor")
	h.realm.GroupMappings = []clusterModels.OIDCGroupMapping{{Claim: "infra", Group: "ops"}}
	h.setRealm(t, nil)

	authURL, _, err := h.svc.StartOIDCLogin(context.Background(), "company", testRedirectURI)
	if err != nil {
		t.Fatal(err)
	}
	query := mustQuery(t, authURL)
	if query.Get("code_challenge_method") != "S256" || query.Get("prompt") != "login" || query.Get("redirect_uri") != testRedirectURI {
		t.Fatalf("auth URL = %s", authURL)
	}

	userID, token, err := h.login(t, map[string]any{"sub": "sub-alice", "preferred_username": "Alice"},
		map[string]any{"groups": []string{"infra"}, "name": "Alice Example"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	claims, err := h.svc.ValidateToken(token)
	if err != nil || claims.AuthType != AuthTypeOIDC || claims.UserID != userID || claims.Username != "alice@company" {
		t.Fatalf("claims = %+v, %v", claims, err)
	}
	var record models.Token
	if err := h.svc.DB.Where("token = ?", token).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if left := time.Until(record.Expiry); left < 7*time.Hour || left > 8*time.Hour {
		t.Fatalf("session lasts %s, want the realm's 8 hours", left)
	}
	if user := h.user(t, "alice@company"); user.FullName != "Alice Example" || user.Source != clusterModels.UserSourceOIDC {
		t.Fatalf("user = %+v", user)
	}
	proxied, err := h.svc.ProxiedUser(userID, AuthTypeOIDC)
	if err != nil || proxied != (ProxiedUser{Username: "alice", AuthType: AuthTypeOIDC, Realm: "company"}) {
		t.Fatalf("proxied = %+v, %v", proxied, err)
	}
	if owner := ProxiedOwner("n1", proxied); owner != "node:n1:oidc:alice@company" {
		t.Fatalf("owner = %s", owner)
	}
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func TestOIDCLoginRefusesBadTokens(t *testing.T) {
	h := newOIDCHarness(t, func(realm *clusterModels.AuthRealm) { realm.AutoCreate = true })
	h.group(t, "everyone", "Auditor")
	h.realm.GroupMappings = []clusterModels.OIDCGroupMapping{{Claim: "staff", Group: "everyone"}}
	h.setRealm(t, nil)
	staff := map[string]any{"groups": []string{"staff"}}

	for _, tc := range []struct {
		name   string
		claims map[string]any
		info   map[string]any
		forge  bool
		want   string
	}{
		{name: "nonce", claims: map[string]any{"nonce": "replayed"}, want: "oidc_id_token_invalid"},
		{name: "audience", claims: map[string]any{"aud": "another-client"}, want: "oidc_id_token_invalid"},
		{name: "authorized party", claims: map[string]any{"azp": "another-client"}, want: "oidc_id_token_invalid"},
		{name: "expired", claims: map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}, want: "oidc_id_token_invalid"},
		{name: "signature", forge: true, want: "oidc_id_token_invalid"},
		{name: "userinfo subject", info: map[string]any{"sub": "someone-else", "groups": []string{"staff"}}, want: "oidc_claims_invalid"},
		{name: "groups", info: map[string]any{"groups": 42}, want: "oidc_claims_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := map[string]any{"sub": "sub-" + tc.name, "preferred_username": "mallory"}
			maps.Copy(claims, tc.claims)
			info := staff
			if tc.info != nil {
				info = tc.info
			}
			h.idp.forge = tc.forge
			defer func() { h.idp.forge = false }()
			_, _, err := h.login(t, claims, info)
			wantLoginError(t, err, tc.want)
			var count int64
			if err := h.svc.DB.Model(&clusterModels.OIDCUser{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("a refused login stored %d users, %v", count, err)
			}
		})
	}
}

func TestOIDCLoginState(t *testing.T) {
	h := newOIDCHarness(t, func(realm *clusterModels.AuthRealm) { realm.AutoCreate = true })
	ctx := context.Background()
	start := func() (string, string, string) {
		authURL, cookie, err := h.svc.StartOIDCLogin(ctx, "company", testRedirectURI)
		if err != nil {
			t.Fatal(err)
		}
		returned, code := h.idp.authorize(t, authURL, map[string]any{"sub": "sub-alice"}, nil)
		return returned, cookie, code
	}
	finish := func(name, state, cookie, code, want string) {
		t.Helper()
		if _, _, err := h.svc.FinishOIDCLogin(ctx, state, cookie, code); err == nil || err.Error() != want {
			t.Fatalf("%s: %v, want %s", name, err, want)
		}
	}

	returned, cookie, code := start()
	_, otherCookie, _ := start()
	sealed, err := base64.RawURLEncoding.DecodeString(cookie)
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 1
	opened, err := h.svc.openLoginState(cookie)
	if err != nil || opened.State != returned || strings.Contains(cookie, opened.Verifier) {
		t.Fatalf("login state = %+v, %v; the cookie must carry it sealed", opened, err)
	}
	finish("missing cookie", returned, "", code, "oidc_login_expired")
	finish("another sign-in's cookie", returned, otherCookie, code, "oidc_login_expired")
	finish("tampered cookie", returned, base64.RawURLEncoding.EncodeToString(sealed), code, "oidc_login_expired")
	// no_permissions means the attempt went through to the account checks.
	finish("the cookie's own attempt", returned, cookie, code, "no_permissions")
	finish("replay", returned, cookie, code, "oidc_exchange_failed")

	returned, cookie, code = start()
	opened, err = h.svc.openLoginState(cookie)
	if err != nil {
		t.Fatal(err)
	}
	opened.Expires = time.Now().Add(-time.Second).Unix()
	stale, err := h.svc.sealLoginState(opened)
	if err != nil {
		t.Fatal(err)
	}
	finish("expired", returned, stale, code, "oidc_login_expired")

	for _, redirect := range []string{"http://node.example/oidc/callback", "https://node.example/elsewhere", "https://node.example/oidc/callback?next=x"} {
		if _, _, err := h.svc.StartOIDCLogin(ctx, "company", redirect); err == nil {
			t.Fatalf("started with redirect %s", redirect)
		}
	}
}

func TestOIDCAccountsAndNames(t *testing.T) {
	h := newOIDCHarness(t, nil)
	ctx := context.Background()

	_, _, err := h.login(t, map[string]any{"sub": "sub-eve", "preferred_username": "eve"}, nil)
	wantLoginError(t, err, "user_not_registered_in_sylve")

	if err := h.svc.CreateRealmUser(ctx, clusterModels.OIDCUser{Realm: "company", Username: "Alice"}); err != nil {
		t.Fatal(err)
	}
	if listed, err := h.svc.ListUsersBySource(clusterModels.UserSourceOIDC); err != nil || len(listed) != 1 || listed[0].Username != "alice@company" {
		t.Fatalf("realm users = %+v, %v", listed, err)
	}
	if err := h.svc.SetGrant(ctx, clusterModels.ACLGrant{Path: "/", SubjectType: clusterModels.ACLSubjectUser, Subject: "alice@company", Role: "Auditor", Propagate: true}); err != nil {
		t.Fatalf("grant to a pre-created user: %v", err)
	}
	if _, _, err := h.login(t, map[string]any{"sub": "sub-alice", "preferred_username": "alice"}, nil); err != nil {
		t.Fatalf("first login of a pre-created user: %v", err)
	}
	_, _, err = h.login(t, map[string]any{"sub": "sub-impostor", "preferred_username": "alice"}, nil)
	wantLoginError(t, err, "user_not_registered_in_sylve")

	h.realm.AutoCreate = true
	h.setRealm(t, nil)
	_, _, err = h.login(t, map[string]any{"sub": "sub-impostor", "preferred_username": "alice"}, nil)
	wantLoginError(t, err, "no_permissions")
	sum := sha256.Sum256([]byte("sub-impostor"))
	h.user(t, "u-"+hex.EncodeToString(sum[:5])+"@company")

	if _, _, err := h.login(t, map[string]any{"sub": "sub-bob", "preferred_username": "Bob@Example.com"}, nil); err == nil || err.Error() != "no_permissions" {
		t.Fatalf("bob: %v", err)
	}
	if err := h.svc.SetGrant(ctx, clusterModels.ACLGrant{Path: "/", SubjectType: clusterModels.ACLSubjectUser, Subject: "bob@example.com@company", Role: "Auditor", Propagate: true}); err != nil {
		t.Fatal(err)
	}
	bob := h.user(t, "bob@example.com@company")
	if principal := PrincipalOf(&bob); principal.Username != "bob@example.com" || principal.Realm != "company" || principal.Superuser {
		t.Fatalf("principal = %+v", principal)
	}
	if !h.hasAccess(t, "bob@example.com@company") {
		t.Fatal("bob's grant doesn't apply")
	}

	if err := h.svc.SetGrant(ctx, clusterModels.ACLGrant{Path: "/", SubjectType: clusterModels.ACLSubjectUser, Subject: "nobody@company", Role: "Auditor"}); err == nil {
		t.Fatal("granted to a realm user who doesn't exist")
	}
}

func TestOIDCGroupMappings(t *testing.T) {
	h := newOIDCHarness(t, func(realm *clusterModels.AuthRealm) { realm.AutoCreate = true })
	h.group(t, "ops", "Auditor")
	h.realm.GroupMappings = []clusterModels.OIDCGroupMapping{{Claim: "infra", Group: "ops"}}
	h.setRealm(t, nil)
	claims := map[string]any{"sub": "sub-alice", "preferred_username": "alice"}

	if _, _, err := h.login(t, claims, map[string]any{"groups": []string{"infra", "unmapped"}}); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetACLGroup(context.Background(), "manual", []string{"alice@company"}); err != nil {
		t.Fatal(err)
	}
	if groups, err := h.svc.groupsOf("alice@company"); err != nil || strings.Join(groups, ",") != "ops,manual" {
		t.Fatalf("groups = %v, %v", groups, err)
	}

	_, _, err := h.login(t, claims, map[string]any{"groups": []string{}})
	wantLoginError(t, err, "no_permissions")
	if groups, err := h.svc.groupsOf("alice@company"); err != nil || strings.Join(groups, ",") != "manual" {
		t.Fatalf("after the IdP removed infra, groups = %v, %v", groups, err)
	}
}

func TestOIDCSessionsFollowLockAndRealm(t *testing.T) {
	h := newOIDCHarness(t, func(realm *clusterModels.AuthRealm) { realm.AutoCreate = true })
	h.group(t, "ops", "Auditor")
	h.realm.GroupMappings = []clusterModels.OIDCGroupMapping{{Claim: "infra", Group: "ops"}}
	h.setRealm(t, nil)
	userID, token, err := h.login(t, map[string]any{"sub": "sub-alice", "preferred_username": "alice"}, map[string]any{"groups": []string{"infra"}})
	if err != nil {
		t.Fatal(err)
	}

	h.realm.Enabled = false
	h.setRealm(t, nil)
	if _, err := h.svc.ValidateToken(token); err == nil {
		t.Fatal("a session outlived its realm being disabled")
	}
	h.realm.Enabled = true
	h.setRealm(t, nil)
	if _, err := h.svc.ValidateToken(token); err != nil {
		t.Fatalf("re-enabled realm: %v", err)
	}

	if err := h.svc.EditUser(userID, EditUserOpts{Username: "alice@company", Password: "new-password"}); err == nil || err.Error() != "external_user_read_only" {
		t.Fatalf("password change: %v", err)
	}
	if err := h.svc.EditUser(userID, EditUserOpts{Username: "alice@company", Locked: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ValidateToken(token); err == nil {
		t.Fatal("a locked user's session still works")
	}
	_, _, err = h.login(t, map[string]any{"sub": "sub-alice", "preferred_username": "alice"}, map[string]any{"groups": []string{"infra"}})
	wantLoginError(t, err, "account_locked")
	if _, _, err := h.svc.CreateJWT("alice@company", "anything", "sylve", false); err == nil {
		t.Fatal("password login worked for a realm user")
	}
	if user := h.user(t, "alice@company"); IsPasskeyRegistrationEligible(user) {
		t.Fatal("a realm user may register a passkey")
	}

	if err := h.svc.DeleteUser(userID, false); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := h.svc.DB.Model(&clusterModels.OIDCUser{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("realm users left = %d, %v", count, err)
	}
}

func TestOIDCHostedDomains(t *testing.T) {
	h := newOIDCHarness(t, func(realm *clusterModels.AuthRealm) {
		realm.AutoCreate, realm.HostedDomains = true, " Example.com  corp.example "
	})
	realms, err := h.svc.ListRealms()
	if err != nil || len(realms) != 1 || realms[0].HostedDomains != "example.com corp.example" {
		t.Fatalf("realms = %+v, %v", realms, err)
	}

	// no_permissions means the domain check passed: the account exists but holds nothing.
	_, _, err = h.login(t, map[string]any{"sub": "sub-ok", "hd": "example.com"}, nil)
	wantLoginError(t, err, "no_permissions")
	for name, claims := range map[string]map[string]any{
		"no hd":      {"sub": "sub-personal"},
		"other hd":   {"sub": "sub-other", "hd": "evil.example"},
		"email only": {"sub": "sub-email", "email": "mallory@example.com", "email_verified": true},
	} {
		_, _, err := h.login(t, claims, nil)
		if err == nil || err.Error() != "oidc_domain_not_allowed" {
			t.Fatalf("%s: %v", name, err)
		}
	}
	_, _, err = h.login(t, map[string]any{"sub": "sub-userinfo"}, map[string]any{"hd": "example.com"})
	wantLoginError(t, err, "oidc_domain_not_allowed")

	h.realm.HostedDomains = "not_a_domain"
	if err := h.svc.SetRealm(context.Background(), clusterModels.AuthRealmSet{AuthRealm: h.realm}); err == nil || err.Error() != "invalid_hosted_domain" {
		t.Fatalf("invalid domain: %v", err)
	}
}

func TestOIDCKeepsOnlySaneProfileClaims(t *testing.T) {
	h := newOIDCHarness(t, func(realm *clusterModels.AuthRealm) { realm.AutoCreate = true })
	_, _, err := h.login(t, map[string]any{
		"sub": "sub-mallory", "preferred_username": "mallory",
		"email": `<img src=x onerror=alert(1)>`, "name": strings.Repeat("M", 257),
	}, nil)
	wantLoginError(t, err, "no_permissions")
	if user := h.user(t, "mallory@company"); user.Email != "" || user.FullName != "" {
		t.Fatalf("stored email %q, name of %d bytes", user.Email, len(user.FullName))
	}

	_, _, err = h.login(t, map[string]any{"sub": "sub-bob", "preferred_username": "bob", "email": "bob@example.com", "name": "Bob"}, nil)
	wantLoginError(t, err, "no_permissions")
	if user := h.user(t, "bob@company"); user.Email != "bob@example.com" || user.FullName != "Bob" {
		t.Fatalf("bob = %q %q", user.Email, user.FullName)
	}
}

func TestOIDCClientDoesNotFollowRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example/token", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	client, err := oidcHTTPClient("")
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Post(server.URL, "application/x-www-form-urlencoded", strings.NewReader("client_secret=s3cret"))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want the redirect itself", response.StatusCode)
	}
}

func TestClaimGroupsTakesObjectKeys(t *testing.T) {
	claims := map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{
		"ops":   map[string]any{"123": "example.com"},
		"admin": map[string]any{"123": "example.com"},
	}}
	groups, err := claimGroups(claims, "urn:zitadel:iam:org:project:roles")
	if err != nil || !slices.Equal(groups, []string{"admin", "ops"}) {
		t.Fatalf("groups = %v, %v", groups, err)
	}
}

func TestOIDCAuthorizationContext(t *testing.T) {
	h := newOIDCHarness(t, func(realm *clusterModels.AuthRealm) {
		realm.AutoCreate, realm.ACRValues, realm.QueryUserInfo = true, "mfa", false
	})
	h.group(t, "ops", "Auditor")
	h.realm.GroupMappings = []clusterModels.OIDCGroupMapping{{Claim: "infra", Group: "ops"}}
	h.setRealm(t, nil)

	authURL, _, err := h.svc.StartOIDCLogin(context.Background(), "company", testRedirectURI)
	if err != nil || mustQuery(t, authURL).Get("acr_values") != "mfa" {
		t.Fatalf("auth URL = %s, %v", authURL, err)
	}
	_, _, err = h.login(t, map[string]any{"sub": "sub-alice", "groups": []string{"infra"}}, nil)
	wantLoginError(t, err, "oidc_acr_not_met")
	if _, _, err := h.login(t, map[string]any{"sub": "sub-alice", "acr": "mfa", "groups": []string{"infra"}}, nil); err != nil {
		t.Fatalf("login with the required acr: %v", err)
	}
	if h.idp.userInfoCalls != 0 {
		t.Fatalf("UserInfo was queried %d times with QueryUserInfo off", h.idp.userInfoCalls)
	}
}

func TestParseProxiedOIDCUser(t *testing.T) {
	user := ProxiedUser{Username: "alice@example.com", AuthType: AuthTypeOIDC, Realm: "company"}
	if got, err := ParseProxiedUser(user.Header()); err != nil || got != user {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	if principal := ForwardedPrincipal(user); principal.Subject() != "alice@example.com@company" || principal.Superuser {
		t.Fatalf("principal = %+v", principal)
	}
	for _, realm := range []string{"", "pam", "sylve", "Bad Realm"} {
		user.Realm = realm
		if _, err := ParseProxiedUser(user.Header()); err == nil {
			t.Fatalf("accepted an oidc user in realm %q", realm)
		}
	}
}

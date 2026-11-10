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
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	OIDCStateCookie  = "sylve_oidc_state"
	OIDCCallbackPath = "/oidc/callback"
	OIDCLoginTTL     = 10 * time.Minute

	oidcMaxGroups = 256
)

var (
	realmNamePattern    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	externalNamePattern = regexp.MustCompile(`^[a-z0-9_][a-z0-9._@+-]{0,63}$`)
)

func validRealmName(name string) bool {
	return realmNamePattern.MatchString(name) && name != RealmPAM && name != RealmSylve
}

// oidcLoginState is what a sign-in needs between start and callback. It travels sealed in the
// browser's cookie, so the node holds nothing an unauthenticated flood could fill.
type oidcLoginState struct {
	State       string `json:"s"`
	Realm       string `json:"r"`
	Verifier    string `json:"v"`
	Nonce       string `json:"n"`
	RedirectURI string `json:"u"`
	Expires     int64  `json:"e"`
}

// The key comes from the node's JWT secret under its own label, so a cookie can't be read or forged,
// and a new JWT secret just cancels the sign-ins in progress.
func (s *Service) loginStateAEAD() (cipher.AEAD, error) {
	secret, err := s.GetJWTSecret()
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("sylve-oidc-login-state"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Service) sealLoginState(state oidcLoginState) (string, error) {
	aead, err := s.loginStateAEAD()
	if err != nil {
		return "", err
	}
	plain, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, nil)), nil
}

func (s *Service) openLoginState(cookie string) (oidcLoginState, error) {
	var state oidcLoginState
	aead, err := s.loginStateAEAD()
	if err != nil {
		return state, err
	}
	sealed, err := base64.RawURLEncoding.DecodeString(cookie)
	if err != nil || len(sealed) < aead.NonceSize() {
		return state, fmt.Errorf("invalid_login_state")
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], nil)
	if err != nil {
		return state, fmt.Errorf("invalid_login_state")
	}
	return state, json.Unmarshal(plain, &state)
}

type oidcProvider struct {
	key      string
	provider *oidc.Provider
	client   *http.Client
}

func (s *Service) realmEnabled(name string) bool {
	var count int64
	err := s.DB.Model(&clusterModels.AuthRealm{}).Where("name = ? AND enabled", name).Count(&count).Error
	return err == nil && count > 0
}

func (s *Service) enabledRealm(name string) (clusterModels.AuthRealm, error) {
	var realm clusterModels.AuthRealm
	if err := s.DB.Where("name = ? AND enabled", name).First(&realm).Error; err != nil {
		return realm, fmt.Errorf("oidc_realm_unavailable")
	}
	return realm, nil
}

func oidcHTTPClient(caCert string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caCert != "" {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(caCert)) {
			return nil, fmt.Errorf("invalid_ca_cert")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	// A redirect would re-send a client_secret_post body to wherever it points; the endpoints come
	// from discovery and have no reason to redirect.
	return &http.Client{
		Transport: transport, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

func discoverProvider(ctx context.Context, issuer, caCert string) (*oidcProvider, error) {
	client, err := oidcHTTPClient(caCert)
	if err != nil {
		return nil, err
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, client), issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc_provider_unavailable: %w", err)
	}
	return &oidcProvider{key: issuer + "\x00" + caCert, provider: provider, client: client}, nil
}

func (s *Service) oidcProvider(ctx context.Context, realm *clusterModels.AuthRealm) (*oidcProvider, error) {
	s.oidcMu.Lock()
	cached := s.oidcProviders[realm.Name]
	s.oidcMu.Unlock()
	if cached != nil && cached.key == realm.Issuer+"\x00"+realm.CACert {
		return cached, nil
	}
	provider, err := discoverProvider(ctx, realm.Issuer, realm.CACert)
	if err != nil {
		return nil, err
	}
	s.oidcMu.Lock()
	s.oidcProviders[realm.Name] = provider
	s.oidcMu.Unlock()
	return provider, nil
}

func oauthConfig(realm *clusterModels.AuthRealm, provider *oidc.Provider, redirectURI string) *oauth2.Config {
	endpoint := provider.Endpoint()
	// A public client sends its ID in the form; basic auth with an empty secret is refused by some.
	if realm.ClientSecret == "" {
		endpoint.AuthStyle = oauth2.AuthStyleInParams
	}
	return &oauth2.Config{
		ClientID: realm.ClientID, ClientSecret: realm.ClientSecret, Endpoint: endpoint,
		RedirectURL: redirectURI, Scopes: strings.Fields(realm.Scopes),
	}
}

// The browser names its own callback URL; the provider only redirects to registered ones.
func validRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Path == OIDCCallbackPath &&
		u.RawQuery == "" && u.Fragment == ""
}

// StartOIDCLogin returns the provider URL and the sealed login state for the cookie.
func (s *Service) StartOIDCLogin(ctx context.Context, realmName, redirectURI string) (string, string, error) {
	realm, err := s.enabledRealm(realmName)
	if err != nil {
		return "", "", err
	}
	if !validRedirectURI(redirectURI) {
		return "", "", fmt.Errorf("invalid_redirect_uri")
	}
	provider, err := s.oidcProvider(ctx, &realm)
	if err != nil {
		logger.L.Warn().Err(err).Str("realm", realm.Name).Msg("oidc_discovery_failed")
		return "", "", fmt.Errorf("oidc_provider_unavailable")
	}

	state, nonce, verifier := rand.Text(), rand.Text(), oauth2.GenerateVerifier()
	cookie, err := s.sealLoginState(oidcLoginState{
		State: state, Realm: realm.Name, Verifier: verifier, Nonce: nonce, RedirectURI: redirectURI,
		Expires: time.Now().Add(OIDCLoginTTL).Unix(),
	})
	if err != nil {
		return "", "", err
	}

	options := []oauth2.AuthCodeOption{oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)}
	if realm.Prompt != "" {
		options = append(options, oauth2.SetAuthURLParam("prompt", realm.Prompt))
	}
	if realm.ACRValues != "" {
		options = append(options, oauth2.SetAuthURLParam("acr_values", realm.ACRValues))
	}
	return oauthConfig(&realm, provider.provider, redirectURI).AuthCodeURL(state, options...), cookie, nil
}

func oidcRefusal(realm, subject, reason string, err error) error {
	logger.L.Warn().Err(err).Str("realm", realm).Str("subject", subject).Str("reason", reason).Msg("oidc_login_refused")
	return fmt.Errorf("%s", reason)
}

// The cookie can be presented again until it expires, but the provider's code works once and the
// nonce ties the ID token to this attempt, so a replay gets nothing.
func (s *Service) FinishOIDCLogin(ctx context.Context, state, cookie, code string) (uint, string, error) {
	pending, err := s.openLoginState(cookie)
	if err != nil || subtle.ConstantTimeCompare([]byte(pending.State), []byte(state)) != 1 ||
		time.Now().Unix() > pending.Expires {
		return 0, "", fmt.Errorf("oidc_login_expired")
	}
	realm, err := s.enabledRealm(pending.Realm)
	if err != nil {
		return 0, "", err
	}
	provider, err := s.oidcProvider(ctx, &realm)
	if err != nil {
		return 0, "", oidcRefusal(realm.Name, "", "oidc_provider_unavailable", err)
	}

	ctx = oidc.ClientContext(ctx, provider.client)
	token, err := oauthConfig(&realm, provider.provider, pending.RedirectURI).Exchange(ctx, code, oauth2.VerifierOption(pending.Verifier))
	if err != nil {
		return 0, "", oidcRefusal(realm.Name, "", "oidc_exchange_failed", err)
	}
	rawIDToken, _ := token.Extra("id_token").(string)
	idToken, err := provider.provider.Verifier(&oidc.Config{ClientID: realm.ClientID}).Verify(ctx, rawIDToken)
	if err != nil {
		return 0, "", oidcRefusal(realm.Name, "", "oidc_id_token_invalid", err)
	}
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		return 0, "", oidcRefusal(realm.Name, idToken.Subject, "oidc_claims_invalid", err)
	}
	if azp, ok := claims["azp"].(string); idToken.Nonce != pending.Nonce || (ok && azp != realm.ClientID) {
		return 0, "", oidcRefusal(realm.Name, idToken.Subject, "oidc_id_token_invalid", nil)
	}
	// The browser carries acr_values to the provider and could drop them, so check what it did.
	if acr, _ := claims["acr"].(string); realm.ACRValues != "" && !slices.Contains(strings.Fields(realm.ACRValues), acr) {
		return 0, "", oidcRefusal(realm.Name, idToken.Subject, "oidc_acr_not_met", nil)
	}
	// Google's hd names the account's Workspace domain; an email address alone isn't proof, since
	// anyone can register a Google account under any address they can verify.
	if hd, _ := claims["hd"].(string); realm.HostedDomains != "" && !slices.Contains(strings.Fields(realm.HostedDomains), strings.ToLower(hd)) {
		return 0, "", oidcRefusal(realm.Name, idToken.Subject, "oidc_domain_not_allowed", nil)
	}
	if realm.QueryUserInfo && provider.provider.UserInfoEndpoint() != "" {
		info, err := provider.provider.UserInfo(ctx, oauth2.StaticTokenSource(token))
		if err != nil {
			return 0, "", oidcRefusal(realm.Name, idToken.Subject, "oidc_userinfo_failed", err)
		}
		extra := map[string]any{}
		if err := info.Claims(&extra); err != nil || info.Subject != idToken.Subject {
			return 0, "", oidcRefusal(realm.Name, idToken.Subject, "oidc_claims_invalid", err)
		}
		for name, value := range extra {
			if _, signed := claims[name]; !signed {
				claims[name] = value
			}
		}
	}

	groups, err := claimGroups(claims, realm.GroupsClaim)
	if err != nil {
		return 0, "", oidcRefusal(realm.Name, idToken.Subject, "oidc_claims_invalid", err)
	}
	if groups == nil && realm.GroupsClaim != "" {
		logger.L.Warn().Str("realm", realm.Name).Str("claim", realm.GroupsClaim).Msg("oidc_groups_claim_missing")
	}
	claimed, _ := claims[realm.UsernameClaim].(string)
	username, err := s.oidcUsername(realm.Name, idToken.Subject, strings.ToLower(claimed))
	if err != nil {
		return 0, "", oidcRefusal(realm.Name, idToken.Subject, "oidc_login_failed", err)
	}
	// The provider's users may set these themselves; keep only sane values.
	name, _ := claims["name"].(string)
	email, _ := claims["email"].(string)
	if len(name) > 256 {
		name = ""
	}
	if len(email) > 256 || !utils.IsValidEmail(email) {
		email = ""
	}
	login := clusterModels.OIDCUser{
		Realm: realm.Name, Subject: idToken.Subject, Username: username, FullName: name, Email: email,
		Groups: groups, IssuedAt: idToken.IssuedAt.Unix(),
	}
	if err := s.propose(ctx, "oidc_user", "login", login); err != nil {
		switch {
		case strings.Contains(err.Error(), "oidc_user_not_registered"):
			return 0, "", oidcRefusal(realm.Name, idToken.Subject, "user_not_registered_in_sylve", err)
		case strings.Contains(err.Error(), "oidc_username_taken"), strings.Contains(err.Error(), "oidc_login_stale"):
			return 0, "", oidcRefusal(realm.Name, idToken.Subject, "oidc_login_conflict", err)
		}
		return 0, "", oidcRefusal(realm.Name, idToken.Subject, "cluster_write_failed", err)
	}

	var account clusterModels.OIDCUser
	var user models.User
	if err := s.DB.Where("realm = ? AND subject = ?", realm.Name, idToken.Subject).First(&account).Error; err != nil {
		return 0, "", oidcRefusal(realm.Name, idToken.Subject, "oidc_login_pending", err)
	}
	if err := s.DB.Where("username = ? AND source = ?", account.Principal(), clusterModels.UserSourceOIDC).First(&user).Error; err != nil {
		return 0, "", oidcRefusal(realm.Name, idToken.Subject, "oidc_login_pending", err)
	}
	if user.Locked {
		return 0, "", oidcRefusal(realm.Name, idToken.Subject, "account_locked", nil)
	}
	if reason, err := s.loginRefusal(&user); err != nil {
		return 0, "", oidcRefusal(realm.Name, idToken.Subject, reason, err)
	}
	session, err := s.issueJWTUntil(user, AuthTypeOIDC, time.Now().Add(time.Duration(realm.SessionHours)*time.Hour))
	if err != nil {
		return 0, "", err
	}
	return user.ID, session, nil
}

// claimGroups reads the groups claim: a string, a list of strings, or an object whose keys are the
// groups (Zitadel's roles). A missing claim is no groups.
func claimGroups(claims map[string]any, name string) ([]string, error) {
	var groups []string
	switch value := claims[name].(type) {
	case nil:
		return nil, nil
	case string:
		groups = []string{value}
	case map[string]any:
		groups = slices.Sorted(maps.Keys(value))
	case []any:
		groups = make([]string, 0, len(value))
		for _, item := range value {
			group, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("groups_claim_not_strings")
			}
			groups = append(groups, group)
		}
	default:
		return nil, fmt.Errorf("groups_claim_not_a_list")
	}
	if len(groups) > oidcMaxGroups || slices.ContainsFunc(groups, func(group string) bool { return len(group) > 256 }) {
		return nil, fmt.Errorf("groups_claim_too_large")
	}
	return groups, nil
}

// A user already bound to the subject keeps their name whatever this returns.
func (s *Service) oidcUsername(realm, subject, claimed string) (string, error) {
	if externalNamePattern.MatchString(claimed) {
		var holder clusterModels.OIDCUser
		if err := s.DB.Where("realm = ? AND username = ?", realm, claimed).Limit(1).Find(&holder).Error; err != nil {
			return "", err
		}
		if holder.ID == 0 || holder.Subject == "" || holder.Subject == subject {
			return claimed, nil
		}
	}
	sum := sha256.Sum256([]byte(subject))
	return "u-" + hex.EncodeToString(sum[:5]), nil
}

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
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"gorm.io/gorm"
)

type LoginRealm struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Default bool   `json:"default"`
}

type RealmDiscovery struct {
	AuthorizationEndpoint string   `json:"authorizationEndpoint"`
	TokenEndpoint         string   `json:"tokenEndpoint"`
	UserInfoEndpoint      string   `json:"userInfoEndpoint"`
	ScopesSupported       []string `json:"scopesSupported"`
	ClaimsSupported       []string `json:"claimsSupported"`
	ACRValuesSupported    []string `json:"acrValuesSupported"`
}

var realmValidationErrors = []string{
	"invalid_realm_name", "invalid_issuer", "invalid_client_id", "scopes_need_openid", "invalid_username_claim",
	"invalid_session_hours", "invalid_ca_cert", "invalid_group_mapping", "unknown_group", "auth_realm_issuer_fixed",
	"unknown_realm", "invalid_username", "username_already_exists", "invalid_hosted_domain",
}

var domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

func IsRealmValidationError(err error) bool {
	return slices.Contains(realmValidationErrors, err.Error())
}

func (s *Service) LoginRealms() ([]LoginRealm, error) {
	var realms []clusterModels.AuthRealm
	if err := s.DB.Where("enabled").Order("name").Find(&realms).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_realms: %w", err)
	}
	list := make([]LoginRealm, 0, len(realms))
	for _, realm := range realms {
		list = append(list, LoginRealm{Name: realm.Name, Label: realm.Label, Default: realm.IsDefault})
	}
	return list, nil
}

func (s *Service) ListRealms() ([]clusterModels.AuthRealm, error) {
	realms := []clusterModels.AuthRealm{}
	if err := s.DB.Order("name").Find(&realms).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_realms: %w", err)
	}
	for i := range realms {
		realms[i].HasSecret = realms[i].ClientSecret != ""
	}
	return realms, nil
}

func validIssuer(issuer string) bool {
	u, err := url.Parse(issuer)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func (s *Service) SetRealm(ctx context.Context, set clusterModels.AuthRealmSet) error {
	realm := &set.AuthRealm
	switch {
	case !validRealmName(realm.Name):
		return fmt.Errorf("invalid_realm_name")
	case !validIssuer(realm.Issuer):
		return fmt.Errorf("invalid_issuer")
	case strings.TrimSpace(realm.ClientID) == "":
		return fmt.Errorf("invalid_client_id")
	case !slices.Contains(strings.Fields(realm.Scopes), "openid"):
		return fmt.Errorf("scopes_need_openid")
	case strings.TrimSpace(realm.UsernameClaim) == "":
		return fmt.Errorf("invalid_username_claim")
	case realm.SessionHours < 1 || realm.SessionHours > 168:
		return fmt.Errorf("invalid_session_hours")
	}
	domains := strings.Fields(strings.ToLower(realm.HostedDomains))
	if slices.ContainsFunc(domains, func(domain string) bool { return !domainPattern.MatchString(domain) }) {
		return fmt.Errorf("invalid_hosted_domain")
	}
	realm.HostedDomains = strings.Join(domains, " ")
	if realm.CACert != "" {
		if _, err := oidcHTTPClient(realm.CACert); err != nil {
			return err
		}
	}
	for _, mapping := range realm.GroupMappings {
		if mapping.Claim == "" || mapping.Group == "" {
			return fmt.Errorf("invalid_group_mapping")
		}
		var count int64
		if err := s.DB.Model(&clusterModels.ACLGroup{}).Where("name = ?", mapping.Group).Count(&count).Error; err != nil {
			return fmt.Errorf("failed_to_load_groups: %w", err)
		}
		if count == 0 {
			return fmt.Errorf("unknown_group")
		}
	}
	if strings.TrimSpace(realm.Label) == "" {
		realm.Label = realm.Name
	}
	var current clusterModels.AuthRealm
	err := s.DB.Where("name = ?", realm.Name).First(&current).Error
	if err == nil && (current.Issuer != realm.Issuer || current.ClientID != realm.ClientID) {
		return fmt.Errorf("auth_realm_issuer_fixed")
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed_to_load_realm: %w", err)
	}
	return s.propose(ctx, "auth_realm", "set", set)
}

func (s *Service) DeleteRealm(ctx context.Context, name string) error {
	return s.propose(ctx, "auth_realm", "delete", clusterModels.AuthRealm{Name: name})
}

// A client secret can't be checked here, only by a real sign-in.
func (s *Service) DiscoverRealm(ctx context.Context, issuer, caCert string) (RealmDiscovery, error) {
	if !validIssuer(issuer) {
		return RealmDiscovery{}, fmt.Errorf("invalid_issuer")
	}
	provider, err := discoverProvider(ctx, issuer, caCert)
	if err != nil {
		return RealmDiscovery{}, err
	}
	var document struct {
		Scopes []string `json:"scopes_supported"`
		Claims []string `json:"claims_supported"`
		ACRs   []string `json:"acr_values_supported"`
	}
	if err := provider.provider.Claims(&document); err != nil {
		return RealmDiscovery{}, fmt.Errorf("oidc_provider_unavailable: %w", err)
	}
	endpoint := provider.provider.Endpoint()
	return RealmDiscovery{
		AuthorizationEndpoint: endpoint.AuthURL, TokenEndpoint: endpoint.TokenURL,
		UserInfoEndpoint: provider.provider.UserInfoEndpoint(), ScopesSupported: document.Scopes,
		ClaimsSupported: document.Claims, ACRValuesSupported: document.ACRs,
	}, nil
}

// The first login whose username claim names them binds their subject.
func (s *Service) CreateRealmUser(ctx context.Context, user clusterModels.OIDCUser) error {
	user.Username = strings.ToLower(strings.TrimSpace(user.Username))
	if !externalNamePattern.MatchString(user.Username) {
		return fmt.Errorf("invalid_username")
	}
	var count int64
	if err := s.DB.Model(&clusterModels.AuthRealm{}).Where("name = ?", user.Realm).Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_load_realm: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("unknown_realm")
	}
	if err := s.DB.Model(&clusterModels.OIDCUser{}).Where("realm = ? AND username = ?", user.Realm, user.Username).Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_username: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("username_already_exists")
	}
	return s.propose(ctx, "oidc_user", "create", clusterModels.OIDCUser{
		Realm: user.Realm, Username: user.Username, FullName: user.FullName, Email: user.Email, Notes: user.Notes,
	})
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
)

var sidPattern = regexp.MustCompile(`^S-1-[0-9]+(?:-[0-9]+){1,15}$`)

const (
	principalSearchLimit          = 25
	principalSearchCandidateLimit = 100
	principalSearchDomainLimit    = 33
)

func principalNameValid(name string) bool {
	return name != "" && len(name) <= 512 && !strings.ContainsAny(name, "\x00\r\n\t\"[]%") && !strings.HasPrefix(name, "-")
}

func (s *Service) ResolvePrincipal(ctx context.Context, value string) (directoryModels.Principal, error) {
	ad, err := s.GetAD()
	if err != nil {
		return directoryModels.Principal{}, err
	}
	if !ad.Joined || !ad.Enabled || s.RecoveryPending() {
		return directoryModels.Principal{}, fmt.Errorf("%w: AD must be joined and enabled", ErrUnavailable)
	}
	if !principalNameValid(value) {
		return directoryModels.Principal{}, invalid("invalid directory principal")
	}
	sid := value
	if !sidPattern.MatchString(sid) {
		if !strings.Contains(value, "\\") && !strings.Contains(value, "@") {
			value = ad.Workgroup + "\\" + value
		}
		out, err := s.command(ctx, "/usr/local/bin/wbinfo", []string{"--name-to-sid=" + value}, "", nil)
		if err != nil {
			return directoryModels.Principal{}, err
		}
		fields := strings.Fields(out)
		if len(fields) == 0 || !sidPattern.MatchString(fields[0]) {
			return directoryModels.Principal{}, invalid("directory principal not found")
		}
		sid = fields[0]
	}
	out, err := s.command(ctx, "/usr/local/bin/wbinfo", []string{"--sid-to-name=" + sid}, "", nil)
	if err != nil {
		return directoryModels.Principal{}, err
	}
	out = strings.TrimSpace(out)
	// The type is the final field; names may contain spaces.
	i := strings.LastIndex(out, " ")
	if i < 0 {
		return directoryModels.Principal{}, invalid("Winbind returned an invalid domain identity")
	}
	name, typeText := strings.TrimSpace(out[:i]), strings.TrimSpace(out[i+1:])
	if !principalNameValid(name) || !strings.Contains(name, "\\") {
		return directoryModels.Principal{}, invalid("Winbind returned an invalid domain identity")
	}
	kind, flag := "", ""
	switch typeText {
	case "1":
		kind, flag = "user", "--sid-to-uid="
	case "2", "4", "5":
		kind, flag = "group", "--sid-to-gid="
	default:
		return directoryModels.Principal{}, invalid("SID is not a user or group")
	}
	domain, _, _ := strings.Cut(name, "\\")
	mapping := directoryModels.IDMap{RangeLow: ad.RangeLow, RangeHigh: ad.RangeHigh}
	if !strings.EqualFold(domain, ad.Workgroup) {
		if !ad.AllowTrustedDomains {
			return directoryModels.Principal{}, invalid("trusted domains are disabled")
		}
		if ad.IDMapBackend != "autorid" {
			index := slices.IndexFunc(ad.TrustedDomains, func(m directoryModels.IDMap) bool { return strings.EqualFold(m.Domain, domain) })
			if index < 0 {
				return directoryModels.Principal{}, invalid("configure an ID mapping for domain %s first", domain)
			}
			mapping = ad.TrustedDomains[index]
		}
	}
	out, err = s.command(ctx, "/usr/local/bin/wbinfo", []string{flag + sid}, "", nil)
	if err != nil {
		return directoryModels.Principal{}, fmt.Errorf("cannot map %s to a Unix %s: %w", name, kind, err)
	}
	id, err := strconv.ParseUint(strings.TrimSpace(out), 10, 32)
	if err != nil || uint32(id) < mapping.RangeLow || uint32(id) > mapping.RangeHigh {
		return directoryModels.Principal{}, invalid("directory identity %s has no Unix ID in its configured range", name)
	}
	return directoryModels.Principal{SID: sid, Name: name, Kind: kind, UnixID: uint32(id)}, nil
}

// An exact lookup also works with enumeration/caching disabled. Search results
// are bounded; domain accounts are never imported into Sylve's login tables.
func (s *Service) SearchPrincipals(ctx context.Context, query, kind string) ([]directoryModels.Principal, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	query = strings.TrimSpace(query)
	if kind != "user" && kind != "group" {
		return nil, invalid("kind must be user or group")
	}
	if query != "" && !principalNameValid(query) {
		return nil, invalid("invalid directory search")
	}
	ad, err := s.GetAD()
	if err != nil {
		return nil, err
	}
	if !ad.Enabled || !ad.Joined || ad.MembershipPending != "" || s.RecoveryPending() {
		s.principalCache.clear()
		return nil, fmt.Errorf("%w: AD is not active", ErrUnavailable)
	}
	if ad.DisableCache {
		s.principalCache.clear()
		return s.searchPrincipals(ctx, query, kind)
	}
	return s.cachedPrincipalSearch(ctx, ad, query, kind, false)
}

func (s *Service) searchPrincipals(ctx context.Context, query, kind string) ([]directoryModels.Principal, error) {
	ad, err := s.GetAD()
	if err != nil {
		return nil, err
	}
	if !ad.Enabled || !ad.Joined || ad.MembershipPending != "" || s.RecoveryPending() {
		return nil, fmt.Errorf("%w: AD is not active", ErrUnavailable)
	}
	domains, filter := []string{ad.Workgroup}, query
	qualifiedDomain, name, domainQualified := strings.Cut(query, "\\")
	if domainQualified {
		if !netbiosName.MatchString(qualifiedDomain) || name == "" {
			return nil, invalid("use DOMAIN\\name for a qualified directory search")
		}
		domain := strings.ToUpper(qualifiedDomain)
		domains, filter = []string{domain}, name
		if !strings.EqualFold(domain, ad.Workgroup) {
			if !ad.AllowTrustedDomains {
				return nil, invalid("trusted domains are disabled")
			}
			if ad.IDMapBackend != "autorid" && !slices.ContainsFunc(ad.TrustedDomains, func(mapping directoryModels.IDMap) bool { return strings.EqualFold(mapping.Domain, domain) }) {
				return nil, invalid("configure an ID mapping for domain %s first", domain)
			}
		}
	} else if ad.AllowTrustedDomains {
		for _, mapping := range ad.TrustedDomains {
			domains = append(domains, mapping.Domain)
		}
	}
	qualified := domainQualified || strings.Contains(query, "@") || sidPattern.MatchString(query)
	rows := []directoryModels.Principal{}
	if query != "" {
		principal, lookupErr := s.ResolvePrincipal(ctx, query)
		if lookupErr == nil && principal.Kind == kind {
			rows = append(rows, principal)
			if qualified || ad.DisableCache || len(domains) == 1 && !(ad.AllowTrustedDomains && ad.IDMapBackend == "autorid") {
				return rows, nil
			}
		}
		if ad.DisableCache || strings.Contains(query, "@") || sidPattern.MatchString(query) {
			if lookupErr != nil {
				return nil, lookupErr
			}
			return []directoryModels.Principal{}, nil
		}
	}
	if ad.DisableCache {
		return []directoryModels.Principal{}, nil
	}
	if !qualified && ad.AllowTrustedDomains && ad.IDMapBackend == "autorid" {
		out, err := s.command(ctx, "/usr/local/bin/wbinfo", []string{"--trusted-domains"}, "", nil)
		if err != nil {
			return nil, err
		}
		for _, name := range strings.Fields(out) {
			if !netbiosName.MatchString(name) || strings.EqualFold(name, "BUILTIN") || strings.EqualFold(name, ad.ComputerName) || slices.ContainsFunc(domains, func(domain string) bool { return strings.EqualFold(domain, name) }) {
				continue
			}
			domains = append(domains, strings.ToUpper(name))
			if len(domains) >= principalSearchDomainLimit {
				break
			}
		}
	}
	flag := "--domain-users"
	if kind == "group" {
		flag = "--domain-groups"
	}
	searches := make([][]string, len(domains))
	filter = strings.ToLower(filter)
	for i, domain := range domains {
		out, err := s.command(ctx, "/usr/local/bin/wbinfo", []string{flag, "--domain=" + domain}, "", nil)
		if err != nil {
			return nil, err
		}
		for _, name := range strings.Split(out, "\n") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if !strings.Contains(name, "\\") && !strings.Contains(name, "@") {
				name = domain + "\\" + name
			}
			if strings.Contains(strings.ToLower(name), filter) {
				searches[i] = append(searches[i], name)
				if len(searches[i]) >= principalSearchCandidateLimit {
					break
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, principal := range rows {
		seen[principal.SID] = true
	}
	var resolutionErr error
	// Consider one identity per domain at a time so a large primary domain
	// cannot crowd every trusted domain out of the bounded suggestions.
	for len(rows) < principalSearchLimit {
		searched := false
		for i := range searches {
			if len(searches[i]) == 0 {
				continue
			}
			if ctx.Err() != nil {
				return nil, fmt.Errorf("%w: directory search timed out", ErrUnavailable)
			}
			name := searches[i][0]
			searches[i] = searches[i][1:]
			searched = true
			principal, err := s.ResolvePrincipal(ctx, name)
			if err != nil {
				if resolutionErr == nil {
					resolutionErr = err
				}
				continue
			}
			if principal.Kind != kind || seen[principal.SID] {
				continue
			}
			seen[principal.SID] = true
			rows = append(rows, principal)
			if len(rows) >= principalSearchLimit {
				break
			}
		}
		if !searched {
			break
		}
	}
	if len(rows) == 0 && resolutionErr != nil {
		return nil, resolutionErr
	}
	return rows, nil
}

type SharePermissionInput struct {
	ReadSIDs  []string
	WriteSIDs []string
}

func (s *Service) ResolveSharePermissions(ctx context.Context, input SharePermissionInput, previous directoryModels.SharePermissions) (directoryModels.SharePermissions, error) {
	// A directory outage must not hold the membership/share configuration lock
	// for a separate command timeout for every saved identity.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result := directoryModels.SharePermissions{Read: []directoryModels.Principal{}, Write: []directoryModels.Principal{}}
	if len(input.ReadSIDs)+len(input.WriteSIDs) > 128 {
		return result, invalid("too many directory principals")
	}
	seen := map[string]bool{}
	savedPrincipals := append(slices.Clone(previous.Read), previous.Write...)
	for _, group := range []struct {
		sids []string
		dest *[]directoryModels.Principal
	}{{input.WriteSIDs, &result.Write}, {input.ReadSIDs, &result.Read}} {
		for _, sid := range group.sids {
			if !sidPattern.MatchString(sid) {
				return result, invalid("directory permissions require a SID")
			}
			if seen[sid] {
				continue
			}
			seen[sid] = true
			// Retain existing identities during outages. Mapping settings cannot be
			// changed while any saved share references them.
			var principal directoryModels.Principal
			for _, saved := range savedPrincipals {
				if saved.SID == sid {
					principal = saved
					break
				}
			}
			resolved, err := s.ResolvePrincipal(ctx, sid)
			if err == nil {
				if principal.SID != "" && (principal.UnixID != resolved.UnixID || principal.Kind != resolved.Kind) {
					return result, fmt.Errorf("%w: saved SID %s changed its Unix mapping; restore the original ID mapping before editing permissions", ErrConflict, sid)
				}
				principal = resolved
			} else if principal.SID == "" || !errors.Is(err, ErrUnavailable) {
				return result, err
			}
			*group.dest = append(*group.dest, principal)
		}
	}
	return result, nil
}

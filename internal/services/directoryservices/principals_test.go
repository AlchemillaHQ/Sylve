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
	"reflect"
	"strconv"
	"strings"
	"testing"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
)

const userSID = "S-1-5-21-111-222-333-1100"
const groupSID = "S-1-5-21-111-222-333-2100"

func principalCommands(t *testing.T, names map[string]string, ids map[string]string) commandRunner {
	t.Helper()
	return func(_ context.Context, path string, args []string, input string, env []string) (string, error) {
		if path != "/usr/local/bin/wbinfo" || len(args) != 1 || input != "" {
			t.Fatalf("unexpected principal command: %s %v", path, args)
		}
		flag, value, _ := strings.Cut(args[0], "=")
		switch flag {
		case "--sid-to-name":
			if name, ok := names[value]; ok {
				return name, nil
			}
		case "--sid-to-uid", "--sid-to-gid":
			if id, ok := ids[value]; ok {
				return id, nil
			}
		case "--name-to-sid":
			for sid, name := range names {
				if strings.EqualFold(strings.TrimSuffix(strings.TrimSuffix(name, " 1"), " 2"), value) {
					return sid + " 1", nil
				}
			}
		}
		return "not found", errors.New("lookup failed")
	}
}

func TestResolveDomainUsersAndGroupsWithoutLocalAccounts(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	s.run = principalCommands(t, map[string]string{userSID: "EXAMPLE\\alice 1", groupSID: "EXAMPLE\\File Users 2"}, map[string]string{userSID: "1001100", groupSID: "1002100"})
	for _, value := range []string{"alice", "EXAMPLE\\alice", userSID} {
		principal, err := s.ResolvePrincipal(context.Background(), value)
		if err != nil || principal.SID != userSID || principal.Kind != "user" || principal.UnixID != 1001100 {
			t.Fatalf("resolve %s: %+v %v", value, principal, err)
		}
	}
	principal, err := s.ResolvePrincipal(context.Background(), groupSID)
	if err != nil || principal.Name != "EXAMPLE\\File Users" || principal.Kind != "group" || principal.UnixID != 1002100 {
		t.Fatalf("resolve spaced group: %+v %v", principal, err)
	}
}

func TestResolveRejectsUnknownTypesAndOutOfRangeIDs(t *testing.T) {
	for _, test := range []struct{ name, id string }{{"EXAMPLE\\alice 3", "1001100"}, {"EXAMPLE\\alice 1", "1000"}, {"EXAMPLE\\alice 1", "4294967295"}, {"EXAMPLE\\alice 1", "bad"}, {"EXAMPLE\\bad%name 1", "1001100"}} {
		t.Run(test.name+test.id, func(t *testing.T) {
			s := newTestService(t)
			saveTestAD(t, s, true)
			s.run = principalCommands(t, map[string]string{userSID: test.name}, map[string]string{userSID: test.id})
			if _, err := s.ResolvePrincipal(context.Background(), userSID); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted invalid mapping: %v", err)
			}
		})
	}
}

func TestTrustedDomainResolutionRequiresExplicitMapping(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, true)
	s.run = principalCommands(t, map[string]string{userSID: "TRUSTED\\alice 1"}, map[string]string{userSID: "2000001100"})
	if _, err := s.ResolvePrincipal(context.Background(), userSID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted disabled trust: %v", err)
	}
	ad.AllowTrustedDomains = true
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolvePrincipal(context.Background(), userSID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted unmapped trust: %v", err)
	}
	ad.TrustedDomains = []directoryModels.IDMap{{Domain: "TRUSTED", Backend: "rid", RangeLow: 2000000000, RangeHigh: 2099999999}}
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolvePrincipal(context.Background(), userSID); err != nil {
		t.Fatal(err)
	}
}

func TestOutageRetainsSavedPermissionsButRejectsNewSIDs(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	s.run = func(context.Context, string, []string, string, []string) (string, error) {
		return "domain offline", errors.New("timeout")
	}
	previous := directoryModels.SharePermissions{Read: []directoryModels.Principal{{SID: userSID, Name: "EXAMPLE\\alice", Kind: "user", UnixID: 1001100}}, Write: []directoryModels.Principal{{SID: groupSID, Name: "EXAMPLE\\File Users", Kind: "group", UnixID: 1002100}}}
	input := SharePermissionInput{ReadSIDs: []string{userSID, userSID, groupSID}, WriteSIDs: []string{groupSID, groupSID}}
	result, err := s.ResolveSharePermissions(context.Background(), input, previous)
	if err != nil || !reflect.DeepEqual(previous, result) {
		t.Fatalf("outage changed permissions or write-wins: %+v %v", result, err)
	}
	input.ReadSIDs = append(input.ReadSIDs, "S-1-5-21-111-222-333-9999")
	if _, err := s.ResolveSharePermissions(context.Background(), input, previous); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("added unknown principal offline: %v", err)
	}
	if _, err := s.ResolveSharePermissions(context.Background(), SharePermissionInput{ReadSIDs: []string{"EXAMPLE\\alice"}}, previous); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted a name rather than SID: %v", err)
	}
}

func TestPermissionRefreshUsesSIDAndDetectsMappingChanges(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	previous := directoryModels.SharePermissions{Read: []directoryModels.Principal{{SID: userSID, Name: "EXAMPLE\\old-name", Kind: "user", UnixID: 1001100}}}
	s.run = principalCommands(t, map[string]string{userSID: "EXAMPLE\\new-name 1"}, map[string]string{userSID: "1001100"})
	input := SharePermissionInput{ReadSIDs: []string{userSID}}
	result, err := s.ResolveSharePermissions(context.Background(), input, previous)
	if err != nil || result.Read[0].Name != "EXAMPLE\\new-name" || result.Read[0].UnixID != 1001100 {
		t.Fatalf("SID rename: %+v %v", result, err)
	}
	s.run = principalCommands(t, map[string]string{userSID: "EXAMPLE\\new-name 1"}, map[string]string{userSID: "1001101"})
	if _, err := s.ResolveSharePermissions(context.Background(), input, previous); !errors.Is(err, ErrConflict) {
		t.Fatalf("silently reinterpreted stored ACL ID: %v", err)
	}
}

func TestSearchRejectsInvalidInputBeforeReadingConfiguration(t *testing.T) {
	s := &Service{}
	for _, test := range []struct {
		name, query, kind string
	}{
		{"missing kind", "alice", ""},
		{"unsupported kind", "alice", "computer"},
		{"option", "--alice", "user"},
		{"control character", "ali\nce", "user"},
		{"quote", "a\"lice", "group"},
		{"too long", strings.Repeat("a", 513), "group"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := s.SearchPrincipals(context.Background(), test.query, test.kind); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid search accepted: %v", err)
			}
		})
	}
}

func TestSearchExactLookupWorksWithEnumerationDisabled(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, true)
	ad.DisableCache = true
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	s.run = principalCommands(t, map[string]string{userSID: "EXAMPLE\\alice 1"}, map[string]string{userSID: "1001100"})
	rows, err := s.SearchPrincipals(context.Background(), "alice", "user")
	if err != nil || len(rows) != 1 || rows[0].SID != userSID {
		t.Fatalf("exact lookup: %+v %v", rows, err)
	}
	s.run = func(context.Context, string, []string, string, []string) (string, error) {
		return "offline", errors.New("timeout")
	}
	if _, err := s.SearchPrincipals(context.Background(), "alice", "user"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("hid offline lookup as an empty result: %v", err)
	}
}

func TestQualifiedSearchEnumeratesTheConfiguredTrustedDomain(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, true)
	ad.AllowTrustedDomains = true
	ad.TrustedDomains = []directoryModels.IDMap{{Domain: "TRUSTED", Backend: "rid", RangeLow: 2000000000, RangeHigh: 2099999999}}
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	resolve := principalCommands(t, map[string]string{groupSID: "TRUSTED\\File Users 2"}, map[string]string{groupSID: "2000002100"})
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if len(args) == 2 && args[0] == "--domain-groups" {
			if args[1] != "--domain=TRUSTED" {
				t.Fatalf("searched the wrong domain: %v", args)
			}
			return "File Users\nOther Users\n", nil
		}
		return resolve(ctx, path, args, input, env)
	}
	rows, err := s.SearchPrincipals(context.Background(), "trusted\\File", "group")
	if err != nil || len(rows) != 1 || rows[0].SID != groupSID || rows[0].Name != "TRUSTED\\File Users" {
		t.Fatalf("trusted-domain search: %+v %v", rows, err)
	}
	ad.TrustedDomains = nil
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.SearchPrincipals(context.Background(), "trusted\\File", "group"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("enumerated an unmapped trust: %v", err)
	}
}

func TestSearchReportsUnixMappingFailures(t *testing.T) {
	for _, test := range []struct {
		name, id string
		want     error
	}{
		{"unavailable", "", ErrUnavailable},
		{"outside range", "1002100", ErrInvalid},
		{"invalid ID", "bad", ErrInvalid},
	} {
		for _, disableCache := range []bool{false, true} {
			t.Run(test.name+"/disableCache="+strconv.FormatBool(disableCache), func(t *testing.T) {
				s := newTestService(t)
				ad := saveTestAD(t, s, true)
				ad.AllowTrustedDomains, ad.DisableCache = true, disableCache
				ad.TrustedDomains = []directoryModels.IDMap{{Domain: "TRUSTED", Backend: "ad", RangeLow: 2000000000, RangeHigh: 2099999999, Schema: "rfc2307"}}
				if err := s.DB.Save(&ad).Error; err != nil {
					t.Fatal(err)
				}
				ids := map[string]string{}
				if test.id != "" {
					ids[groupSID] = test.id
				}
				resolve := principalCommands(t, map[string]string{groupSID: "TRUSTED\\File Users 2"}, ids)
				s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
					if len(args) == 2 && args[0] == "--domain-groups" {
						if disableCache || args[1] != "--domain=TRUSTED" {
							t.Fatalf("unexpected enumeration: %v", args)
						}
						return "File Users\n", nil
					}
					return resolve(ctx, path, args, input, env)
				}
				query := "TRUSTED\\File"
				if disableCache {
					query += " Users"
				}
				rows, err := s.SearchPrincipals(context.Background(), query, "group")
				if len(rows) != 0 || !errors.Is(err, test.want) || !strings.Contains(err.Error(), "TRUSTED\\File Users") {
					t.Fatalf("hid a known group's Unix mapping failure: %+v %v", rows, err)
				}
			})
		}
	}
}

func TestSearchDistinguishesNoMatchesFromUnmappedMatches(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	const unmappedSID = "S-1-5-21-111-222-333-2101"
	resolve := principalCommands(t, map[string]string{groupSID: "EXAMPLE\\File Users 2", unmappedSID: "EXAMPLE\\File Other 2"}, map[string]string{groupSID: "1002100", unmappedSID: "4294967295"})
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if len(args) == 2 && args[0] == "--domain-groups" && args[1] == "--domain=EXAMPLE" {
			return "File Other\nFile Users\n", nil
		}
		return resolve(ctx, path, args, input, env)
	}
	rows, err := s.SearchPrincipals(context.Background(), "File", "group")
	if err != nil || len(rows) != 1 || rows[0].SID != groupSID {
		t.Fatalf("unmapped group hid a usable match: %+v %v", rows, err)
	}
	rows, err = s.SearchPrincipals(context.Background(), "Missing", "group")
	if err != nil || len(rows) != 0 {
		t.Fatalf("genuine no-match search became an error: %+v %v", rows, err)
	}
}

func principalSearchCommands(t *testing.T, lists map[string][]string, names, ids map[string]string) commandRunner {
	t.Helper()
	resolve := principalCommands(t, names, ids)
	return func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if len(args) == 2 && (args[0] == "--domain-users" || args[0] == "--domain-groups") {
			domain := strings.TrimPrefix(args[1], "--domain=")
			list, ok := lists[domain]
			if path != "/usr/local/bin/wbinfo" || input != "" || !ok {
				t.Fatalf("unexpected domain enumeration: %s %v", path, args)
			}
			return strings.Join(list, "\n"), nil
		}
		return resolve(ctx, path, args, input, env)
	}
}

func TestSearchSuggestionsAndUnqualifiedNamesIncludeMappedTrusts(t *testing.T) {
	const trustedSID = "S-1-5-21-444-555-666-2100"
	for _, kind := range []string{"user", "group"} {
		for _, query := range []string{"", "file", "File Readers"} {
			t.Run(kind+"/"+query, func(t *testing.T) {
				s := newTestService(t)
				ad := saveTestAD(t, s, true)
				ad.AllowTrustedDomains = true
				ad.TrustedDomains = []directoryModels.IDMap{{Domain: "TRUSTED", Backend: "ad", RangeLow: 2000000000, RangeHigh: 2099999999, Schema: "rfc2307"}}
				if err := s.DB.Save(&ad).Error; err != nil {
					t.Fatal(err)
				}
				typeID := "2"
				if kind == "user" {
					typeID = "1"
				}
				s.run = principalSearchCommands(t,
					map[string][]string{"EXAMPLE": {"File Readers", "File Readers"}, "TRUSTED": {"TRUSTED\\File Readers"}},
					map[string]string{groupSID: "EXAMPLE\\File Readers " + typeID, trustedSID: "TRUSTED\\File Readers " + typeID},
					map[string]string{groupSID: "1002100", trustedSID: "2000002100"})
				rows, err := s.SearchPrincipals(context.Background(), query, kind)
				want := []directoryModels.Principal{
					{SID: groupSID, Name: "EXAMPLE\\File Readers", Kind: kind, UnixID: 1002100},
					{SID: trustedSID, Name: "TRUSTED\\File Readers", Kind: kind, UnixID: 2000002100},
				}
				if err != nil || !reflect.DeepEqual(rows, want) {
					t.Fatalf("primary/trusted search: %+v %v", rows, err)
				}
			})
		}
	}
}

func TestSearchSuggestionsAreBoundedAndIncludeTrustedDomains(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, true)
	ad.AllowTrustedDomains = true
	ad.TrustedDomains = []directoryModels.IDMap{{Domain: "TRUSTED", Backend: "rid", RangeLow: 2000000000, RangeHigh: 2099999999}}
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	lists, names, ids := map[string][]string{}, map[string]string{}, map[string]string{}
	for i, domain := range []string{"EXAMPLE", "TRUSTED"} {
		for j := range 150 {
			name := fmt.Sprintf("Group %03d", j)
			sid := fmt.Sprintf("S-1-5-21-111-222-%d-%d", 333+i, 2100+j)
			lists[domain] = append(lists[domain], name)
			names[sid] = domain + "\\" + name + " 2"
			ids[sid] = strconv.FormatUint(uint64(1002100+i*1999000000+j), 10)
		}
	}
	s.run = principalSearchCommands(t, lists, names, ids)
	rows, err := s.SearchPrincipals(context.Background(), "", "group")
	if err != nil || len(rows) != 25 {
		t.Fatalf("bounded suggestions: %+v %v", rows, err)
	}
	for i, row := range rows {
		domain := "EXAMPLE"
		if i%2 == 1 {
			domain = "TRUSTED"
		}
		if !strings.HasPrefix(row.Name, domain+"\\") {
			t.Fatalf("primary domain crowded out the trust: %+v", rows)
		}
	}
}

func TestSearchSuggestionCandidateResolutionIsBounded(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	lists, names, ids := map[string][]string{}, map[string]string{}, map[string]string{}
	for i := range 150 {
		name := fmt.Sprintf("Group %03d", i)
		sid := fmt.Sprintf("S-1-5-21-111-222-333-%d", 2100+i)
		lists["EXAMPLE"] = append(lists["EXAMPLE"], name)
		names[sid] = "EXAMPLE\\" + name + " 2"
		ids[sid] = "1000"
	}
	resolve := principalSearchCommands(t, lists, names, ids)
	lookups := 0
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if len(args) == 1 && strings.HasPrefix(args[0], "--name-to-sid=") {
			lookups++
		}
		return resolve(ctx, path, args, input, env)
	}
	rows, err := s.SearchPrincipals(context.Background(), "", "group")
	if len(rows) != 0 || !errors.Is(err, ErrInvalid) || lookups != 100 {
		t.Fatalf("unbounded or silently empty unmapped suggestions: %+v %v (%d lookups)", rows, err, lookups)
	}
}

func TestSearchBlankRespectsInactiveADAndDisabledEnumeration(t *testing.T) {
	for _, state := range []string{"disabled", "unjoined", "enumeration disabled"} {
		t.Run(state, func(t *testing.T) {
			s := newTestService(t)
			ad := saveTestAD(t, s, true)
			switch state {
			case "disabled":
				ad.Enabled = false
			case "unjoined":
				ad.Joined = false
			case "enumeration disabled":
				ad.DisableCache = true
			}
			if err := s.DB.Save(&ad).Error; err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"user", "group"} {
				rows, err := s.SearchPrincipals(context.Background(), "", kind)
				if len(rows) != 0 || (state == "enumeration disabled" && err != nil) || (state != "enumeration disabled" && !errors.Is(err, ErrUnavailable)) {
					t.Fatalf("blank search ignored AD state: %+v %v", rows, err)
				}
			}
		})
	}
}

func TestSearchDoesNotBrowseDisabledTrusts(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	s.run = principalSearchCommands(t, map[string][]string{"EXAMPLE": {"File Users"}}, map[string]string{groupSID: "EXAMPLE\\File Users 2"}, map[string]string{groupSID: "1002100"})
	rows, err := s.SearchPrincipals(context.Background(), "", "group")
	if err != nil || len(rows) != 1 || rows[0].SID != groupSID {
		t.Fatalf("primary-only suggestions: %+v %v", rows, err)
	}
	if _, err := s.SearchPrincipals(context.Background(), "TRUSTED\\File", "group"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("searched a disabled trust: %v", err)
	}
}

func TestSearchAutoridSuggestionsExcludeBuiltinLocalAndDuplicateDomains(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, true)
	ad.IDMapBackend, ad.AllowTrustedDomains = "autorid", true
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	const trustedSID = "S-1-5-21-444-555-666-2100"
	resolve := principalSearchCommands(t,
		map[string][]string{"EXAMPLE": {"File Users"}, "TRUSTED": {"File Users"}},
		map[string]string{groupSID: "EXAMPLE\\File Users 2", trustedSID: "TRUSTED\\File Users 2"},
		map[string]string{groupSID: "1002100", trustedSID: "1102100"})
	enumerated := []string{}
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if len(args) == 1 && args[0] == "--trusted-domains" {
			return "EXAMPLE\nBUILTIN\nSYLVE\nTRUSTED\ntrusted\nbad/domain\n", nil
		}
		if len(args) == 2 {
			enumerated = append(enumerated, args[1])
		}
		return resolve(ctx, path, args, input, env)
	}
	rows, err := s.SearchPrincipals(context.Background(), "", "group")
	if err != nil || len(rows) != 2 || !reflect.DeepEqual(enumerated, []string{"--domain=EXAMPLE", "--domain=TRUSTED"}) {
		t.Fatalf("AUTORID discovered non-directory or duplicate domains: %+v %v (%v)", rows, err, enumerated)
	}
}

func TestSearchKeepsUsableResultsWhenAnotherDomainHasUnmappedIdentities(t *testing.T) {
	const trustedSID = "S-1-5-21-444-555-666-2100"
	for _, unmappedDomain := range []string{"EXAMPLE", "TRUSTED"} {
		t.Run(unmappedDomain, func(t *testing.T) {
			s := newTestService(t)
			ad := saveTestAD(t, s, true)
			ad.AllowTrustedDomains = true
			ad.TrustedDomains = []directoryModels.IDMap{{Domain: "TRUSTED", Backend: "ad", RangeLow: 2000000000, RangeHigh: 2099999999, Schema: "rfc2307"}}
			if err := s.DB.Save(&ad).Error; err != nil {
				t.Fatal(err)
			}
			ids := map[string]string{groupSID: "1002100", trustedSID: "2000002100"}
			wantSID := trustedSID
			if unmappedDomain == "EXAMPLE" {
				delete(ids, groupSID)
			} else {
				delete(ids, trustedSID)
				wantSID = groupSID
			}
			s.run = principalSearchCommands(t,
				map[string][]string{"EXAMPLE": {"File Users"}, "TRUSTED": {"File Users"}},
				map[string]string{groupSID: "EXAMPLE\\File Users 2", trustedSID: "TRUSTED\\File Users 2"}, ids)
			rows, err := s.SearchPrincipals(context.Background(), "", "group")
			if err != nil || len(rows) != 1 || rows[0].SID != wantSID {
				t.Fatalf("unmapped identities hid usable suggestions: %+v %v", rows, err)
			}
		})
	}
}

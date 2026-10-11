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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
)

func cacheTestResolver(t *testing.T, s *Service) *atomic.Int32 {
	t.Helper()
	commands := &atomic.Int32{}
	resolve := principalSearchCommands(t,
		map[string][]string{"EXAMPLE": {"alice", "File Users"}},
		map[string]string{userSID: "EXAMPLE\\alice 1", groupSID: "EXAMPLE\\File Users 2"},
		map[string]string{userSID: "1001100", groupSID: "1002100"})
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		commands.Add(1)
		return resolve(ctx, path, args, input, env)
	}
	return commands
}

func TestPrincipalCacheReusesSuggestionsQueriesAndEmptyResults(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	commands := cacheTestResolver(t, s)
	for _, query := range []string{"", "File", "Nobody"} {
		rows, err := s.SearchPrincipals(context.Background(), query, "group")
		if err != nil {
			t.Fatal(err)
		}
		before := commands.Load()
		cached, err := s.SearchPrincipals(context.Background(), "  "+strings.ToLower(query)+"  ", "group")
		if err != nil || !reflect.DeepEqual(rows, cached) || commands.Load() != before {
			t.Fatalf("repeated a cached query: %+v %v (%d -> %d)", cached, err, before, commands.Load())
		}
		if len(rows) > 0 {
			rows[0].UnixID = 1
			fresh, err := s.SearchPrincipals(context.Background(), query, "group")
			if err != nil || fresh[0].UnixID == 1 {
				t.Fatalf("caller mutated cached identities: %+v %v", fresh, err)
			}
		}
	}
}

func TestPrincipalCacheExpiresAndInvalidatesForConfigurationChanges(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, true)
	commands := cacheTestResolver(t, s)
	if _, err := s.SearchPrincipals(context.Background(), "", "group"); err != nil {
		t.Fatal(err)
	}
	before := commands.Load()
	s.principalCache.mu.Lock()
	entry := s.principalCache.entries["group:"]
	entry.updatedAt = time.Now().Add(-principalSuggestionCacheTTL)
	s.principalCache.entries["group:"] = entry
	s.principalCache.mu.Unlock()
	if _, err := s.SearchPrincipals(context.Background(), "", "group"); err != nil || commands.Load() <= before {
		t.Fatalf("expired cache was not rebuilt: %v", err)
	}
	before = commands.Load()
	ad.Site = "another-site"
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.SearchPrincipals(context.Background(), "", "group"); err != nil || commands.Load() <= before {
		t.Fatalf("configuration change retained an old cache: %v", err)
	}
}

func TestPrincipalCacheRespectsDisabledCachingAndInactiveMembership(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, true)
	commands := cacheTestResolver(t, s)
	if _, err := s.SearchPrincipals(context.Background(), "", "group"); err != nil {
		t.Fatal(err)
	}
	ad.DisableCache = true
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	before := commands.Load()
	for range 2 {
		if _, err := s.SearchPrincipals(context.Background(), "EXAMPLE\\alice", "user"); err != nil {
			t.Fatal(err)
		}
	}
	if commands.Load() != before+6 || len(s.principalCache.entries) != 0 {
		t.Fatal("Disable Cache reused a directory result")
	}
	for _, state := range []string{"disabled", "unjoined", "join", "leave"} {
		ad.DisableCache, ad.Enabled, ad.Joined, ad.MembershipPending = false, true, true, ""
		switch state {
		case "disabled":
			ad.Enabled = false
		case "unjoined":
			ad.Joined = false
		default:
			ad.MembershipPending = state
		}
		if err := s.DB.Save(&ad).Error; err != nil {
			t.Fatal(err)
		}
		before := commands.Load()
		if _, err := s.SearchPrincipals(context.Background(), "", "group"); !errors.Is(err, ErrUnavailable) || commands.Load() != before {
			t.Fatalf("inactive membership %s ran a directory scan: %v", state, err)
		}
		s.refreshPrincipalSuggestions(context.Background())
		if commands.Load() != before {
			t.Fatalf("background scan ignored membership %s", state)
		}
	}
}

func TestPrincipalCacheSharesScansAndDoesNotCancelOtherWaiters(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	commands := cacheTestResolver(t, s)
	resolve := s.run
	started, release := make(chan struct{}), make(chan struct{})
	var enumerations atomic.Int32
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if len(args) == 2 && args[0] == "--domain-groups" {
			if enumerations.Add(1) == 1 {
				close(started)
			}
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return resolve(ctx, path, args, input, env)
	}
	ctx, cancel := context.WithCancel(context.Background())
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { _, err := s.SearchPrincipals(ctx, "", "group"); first <- err }()
	<-started
	go func() { _, err := s.SearchPrincipals(context.Background(), "", "group"); second <- err }()
	cancel()
	if err := <-first; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cancelled waiter did not return: %v", err)
	}
	close(release)
	if err := <-second; err != nil || enumerations.Load() != 1 || commands.Load() == 0 {
		t.Fatalf("cancelled another waiter or duplicated its scan: %v (%d scans)", err, enumerations.Load())
	}
}

func TestPrincipalCacheBackgroundRefreshPreservesLastGoodAndReportsFailures(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	commands := cacheTestResolver(t, s)
	s.refreshPrincipalSuggestions(context.Background())
	before := commands.Load()
	for _, kind := range []string{"user", "group"} {
		rows, err := s.SearchPrincipals(context.Background(), "", kind)
		if err != nil || len(rows) != 1 || commands.Load() != before {
			t.Fatalf("background suggestions were not cached: %+v %v", rows, err)
		}
	}
	resolve := s.run
	s.run = func(context.Context, string, []string, string, []string) (string, error) {
		return "directory offline", errors.New("lookup failed")
	}
	s.refreshPrincipalSuggestions(context.Background())
	for _, kind := range []string{"user", "group"} {
		rows, err := s.SearchPrincipals(context.Background(), "", kind)
		if len(rows) != 0 || !errors.Is(err, ErrUnavailable) {
			t.Fatalf("failed refresh looked like empty success: %+v %v", rows, err)
		}
		if len(s.principalCache.entries[kind+":"].principals) != 1 {
			t.Fatal("failed refresh discarded the last good snapshot")
		}
	}
	s.run = resolve
	if rows, err := s.SearchPrincipals(context.Background(), "", "group"); err != nil || len(rows) != 1 {
		t.Fatalf("failed background refresh prevented an immediate retry: %+v %v", rows, err)
	}
}

func TestPrincipalCacheRejectsConfigurationChangesDuringScans(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, true)
	cacheTestResolver(t, s)
	resolve := s.run
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if len(args) == 2 && args[0] == "--domain-groups" {
			ad.Site = "changed-site"
			if err := s.DB.Save(&ad).Error; err != nil {
				t.Fatal(err)
			}
		}
		return resolve(ctx, path, args, input, env)
	}
	if _, err := s.SearchPrincipals(context.Background(), "", "group"); !errors.Is(err, ErrUnavailable) || len(s.principalCache.entries) != 0 {
		t.Fatalf("published results from a replaced configuration: %v", err)
	}
}

func TestPrincipalCacheBoundsEntriesAndExcludesCredentials(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, true)
	cacheTestResolver(t, s)
	beforeMapping := principalCacheConfig(ad)
	ad.TrustedDomains = []directoryModels.IDMap{{Domain: "TRUSTED", Backend: "ad", RangeLow: 2000000000, RangeHigh: 2099999999, Schema: "rfc2307"}}
	if principalCacheConfig(ad) == beforeMapping {
		t.Fatal("cache identity ignored trusted mappings")
	}
	ad.JoinUsername, ad.JoinPasswordConfigured = "private-enrollment-marker", true
	ad.JoinPasswordCiphertext = []byte("private-ciphertext-marker")
	key := principalCacheConfig(ad)
	if strings.Contains(key, "private-enrollment-marker") || strings.Contains(key, "private-ciphertext-marker") {
		t.Fatal("cache identity included enrollment credentials")
	}
	if _, err := s.SearchPrincipals(context.Background(), "", "group"); err != nil {
		t.Fatal(err)
	}
	for i := range principalCacheEntryLimit + 10 {
		if _, err := s.SearchPrincipals(context.Background(), fmt.Sprintf("Nobody%d", i), "group"); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.principalCache.entries) > principalCacheEntryLimit || len(s.principalCache.entries["group:"].principals) != 1 {
		t.Fatal("query cache grew without a bound or evicted default suggestions")
	}
}

func TestPrincipalCacheDoesNotAuthorizeSharePermissions(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	cacheTestResolver(t, s)
	if _, err := s.SearchPrincipals(context.Background(), "", "group"); err != nil {
		t.Fatal(err)
	}
	s.run = principalCommands(t, map[string]string{groupSID: "EXAMPLE\\File Users 2"}, map[string]string{groupSID: "1000"})
	if _, err := s.ResolveSharePermissions(context.Background(), SharePermissionInput{ReadSIDs: []string{groupSID}}, directoryModels.SharePermissions{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cached display data authorized a now-unmapped SID: %v", err)
	}
}

func TestPrincipalCacheMonitorRefreshesPeriodicallyAndStopsScans(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	cacheTestResolver(t, s)
	resolve := s.run
	var enumerations atomic.Int32
	refreshed := make(chan struct{})
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if len(args) == 2 && enumerations.Add(1) == 5 {
			close(refreshed)
			<-ctx.Done()
			return "", ctx.Err()
		}
		return resolve(ctx, path, args, input, env)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.monitorPrincipalCache(ctx, 10*time.Millisecond); close(done) }()
	select {
	case <-refreshed:
	case <-time.After(3 * time.Second):
		t.Fatal("periodic suggestions did not refresh")
	}
	s.principalCache.mu.Lock()
	key := fmt.Sprintf("%d\x00%s\x00group:", s.principalCache.generation, s.principalCache.config)
	s.principalCache.mu.Unlock()
	flight := s.principalCache.flights.DoChan(key, func() (any, error) { return nil, nil })
	cancel()
	<-done
	if result := <-flight; !errors.Is(result.Err, ErrUnavailable) {
		t.Fatalf("shutdown did not stop the in-flight scan: %v", result.Err)
	}
}

func TestPrincipalCacheInvalidatesAcrossSavedSettingAndEnableTransitions(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	commands := cacheTestResolver(t, s)
	ctx := context.Background()
	if _, err := s.SearchPrincipals(ctx, "", "group"); err != nil {
		t.Fatal(err)
	}
	before := commands.Load()
	input := testADInput()
	for _, site := range []string{"another-site", ""} {
		input.Site = site
		if _, err := s.SaveAD(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SearchPrincipals(ctx, "", "group"); err != nil || commands.Load() <= before {
		t.Fatalf("changing settings back reused an old snapshot: %v", err)
	}
	before = commands.Load()
	for _, enabled := range []bool{false, true} {
		if err := s.SetEnabled(ctx, enabled); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SearchPrincipals(ctx, "", "group"); err != nil || commands.Load() <= before {
		t.Fatalf("disable/re-enable reused a previous membership snapshot: %v", err)
	}
}

func TestPrincipalCacheMembershipOperationsClearSuggestions(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, false)
	cacheTestResolver(t, s)
	resolve := s.run
	healthy := healthyCommands(t, s, "fixture-password")
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if path == "/usr/local/bin/wbinfo" {
			return resolve(ctx, path, args, input, env)
		}
		return healthy(ctx, path, args, input, env)
	}
	ctx := context.Background()
	for range 2 {
		if err := s.Join(ctx, JoinInput{Username: "joinuser", Password: "fixture-password"}); err != nil {
			t.Fatal(err)
		}
		if len(s.principalCache.entries) != 0 {
			t.Fatal("join retained a previous membership snapshot")
		}
		if _, err := s.SearchPrincipals(ctx, "", "group"); err != nil {
			t.Fatal(err)
		}
		if err := s.Leave(ctx, LeaveInput{LocalOnly: true}); err != nil {
			t.Fatal(err)
		}
		if len(s.principalCache.entries) != 0 {
			t.Fatal("leave retained directory suggestions")
		}
	}
}

func TestPrincipalCacheInvalidationRejectsOldFlightWithSameConfiguration(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	cacheTestResolver(t, s)
	resolve := s.run
	started, release := make(chan struct{}), make(chan struct{}, 1)
	defer close(release)
	var enumerations atomic.Int32
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if len(args) == 2 && args[0] == "--domain-groups" && enumerations.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return resolve(ctx, path, args, input, env)
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { _, err := s.SearchPrincipals(context.Background(), "", "group"); first <- err }()
	<-started
	s.principalCache.clear()
	go func() { _, err := s.SearchPrincipals(context.Background(), "", "group"); second <- err }()
	select {
	case err := <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("new membership search reused an invalidated flight")
	}
	release <- struct{}{}
	if err := <-first; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("invalidated flight published stale results: %v", err)
	}
	if len(s.principalCache.entries["group:"].principals) != 1 || enumerations.Load() != 2 {
		t.Fatal("invalidated flight replaced the new generation's snapshot")
	}
}

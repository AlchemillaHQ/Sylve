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
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	"golang.org/x/sync/singleflight"
)

const (
	principalCacheRefreshInterval = 5 * time.Minute
	principalQueryCacheTTL        = 5 * time.Minute
	principalSuggestionCacheTTL   = 10 * time.Minute
	principalCacheEntryLimit      = 64
)

// Display metadata only: authorization and saved SID validation never use this cache.
type principalSearchCache struct {
	mu         sync.Mutex
	config     string
	generation uint64
	entries    map[string]principalCacheEntry
	flights    singleflight.Group
	ctx        context.Context
}

type principalCacheEntry struct {
	principals    []directoryModels.Principal
	updatedAt     time.Time
	refreshFailed bool
}

func principalCacheConfig(ad directoryModels.ActiveDirectory) string {
	ad.JoinUsername, ad.JoinPasswordConfigured, ad.JoinPasswordCiphertext = "", false, nil
	data, _ := json.Marshal(ad)
	return string(data)
}

func (cache *principalSearchCache) clear() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.config, cache.entries = "", nil
	cache.generation++
}

func (cache *principalSearchCache) prepare(config string) {
	if cache.config != config || cache.entries == nil {
		cache.config = config
		cache.entries = map[string]principalCacheEntry{}
		cache.generation++
	}
}

func (cache *principalSearchCache) result(key string, ttl time.Duration) ([]directoryModels.Principal, bool) {
	entry, found := cache.entries[key]
	if !found || entry.refreshFailed || time.Since(entry.updatedAt) >= ttl {
		return nil, false
	}
	return slices.Clone(entry.principals), true
}

func (s *Service) cachedPrincipalSearch(ctx context.Context, ad directoryModels.ActiveDirectory, query, kind string, refresh bool) ([]directoryModels.Principal, error) {
	cache := &s.principalCache
	config := principalCacheConfig(ad)
	key := kind + ":" + strings.ToLower(strings.TrimSpace(query))
	ttl := principalQueryCacheTTL
	if query == "" {
		ttl = principalSuggestionCacheTTL
	}
	cache.mu.Lock()
	cache.prepare(config)
	if rows, found := cache.result(key, ttl); !refresh && found {
		cache.mu.Unlock()
		return rows, nil
	}
	generation := cache.generation
	scanContext := cache.ctx
	if scanContext == nil {
		scanContext = context.Background()
	}
	cache.mu.Unlock()

	// A browser closing its dropdown must not cancel another request's shared scan.
	result := cache.flights.DoChan(fmt.Sprintf("%d\x00%s\x00%s", generation, config, key), func() (any, error) {
		cache.mu.Lock()
		if cache.config != config || cache.generation != generation {
			cache.mu.Unlock()
			return nil, fmt.Errorf("%w: directory cache was invalidated; retry the search", ErrUnavailable)
		}
		if rows, found := cache.result(key, ttl); !refresh && found {
			cache.mu.Unlock()
			return rows, nil
		}
		cache.mu.Unlock()
		scanContext, cancel := context.WithTimeout(scanContext, 20*time.Second)
		defer cancel()
		rows, err := s.searchPrincipals(scanContext, query, kind)
		if scanContext.Err() != nil {
			err = fmt.Errorf("%w: directory scan stopped: %v", ErrUnavailable, scanContext.Err())
		}
		current, configErr := s.GetAD()
		if configErr != nil {
			return nil, configErr
		}
		if principalCacheConfig(current) != config {
			return nil, fmt.Errorf("%w: AD configuration changed; retry the directory search", ErrUnavailable)
		}
		cache.mu.Lock()
		defer cache.mu.Unlock()
		if cache.config != config || cache.generation != generation {
			return nil, fmt.Errorf("%w: directory cache was invalidated; retry the search", ErrUnavailable)
		}
		if err != nil {
			if previous, exists := cache.entries[key]; exists {
				previous.refreshFailed = true
				cache.entries[key] = previous
			}
			return nil, err
		}
		if _, exists := cache.entries[key]; !exists && len(cache.entries) >= principalCacheEntryLimit {
			oldest := ""
			for candidate, value := range cache.entries {
				if candidate == "user:" || candidate == "group:" {
					continue
				}
				if oldest == "" || value.updatedAt.Before(cache.entries[oldest].updatedAt) {
					oldest = candidate
				}
			}
			delete(cache.entries, oldest)
		}
		cache.entries[key] = principalCacheEntry{principals: slices.Clone(rows), updatedAt: time.Now()}
		return rows, nil
	})
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: directory search cancelled: %v", ErrUnavailable, ctx.Err())
	case result := <-result:
		if result.Err != nil {
			return nil, result.Err
		}
		return slices.Clone(result.Val.([]directoryModels.Principal)), nil
	}
}

func (s *Service) refreshPrincipalSuggestions(ctx context.Context) {
	ad, err := s.GetAD()
	if err != nil {
		return
	}
	if !ad.Enabled || !ad.Joined || ad.DisableCache || ad.MembershipPending != "" || s.RecoveryPending() {
		s.principalCache.clear()
		return
	}
	for _, kind := range []string{"group", "user"} {
		if ctx.Err() != nil {
			return
		}
		_, _ = s.cachedPrincipalSearch(ctx, ad, "", kind, true)
	}
}

// Only configured, active AD is scanned. No credentials or results are persisted.
func (s *Service) MonitorPrincipalCache(ctx context.Context) {
	s.monitorPrincipalCache(ctx, principalCacheRefreshInterval)
}

func (s *Service) monitorPrincipalCache(ctx context.Context, interval time.Duration) {
	s.principalCache.mu.Lock()
	s.principalCache.ctx = ctx
	s.principalCache.mu.Unlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	s.refreshPrincipalSuggestions(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshPrincipalSuggestions(ctx)
		}
	}
}

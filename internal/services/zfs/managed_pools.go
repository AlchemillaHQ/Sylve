// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"fmt"
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	"gorm.io/gorm"
)

type managedPoolScope struct {
	pools   []*gzfs.ZPool
	names   []string
	guids   []string
	nameSet map[string]struct{}
	guidSet map[string]struct{}
}

func (s *Service) loadManagedPoolScope(ctx context.Context) (managedPoolScope, error) {
	scope := managedPoolScope{
		pools:   make([]*gzfs.ZPool, 0),
		names:   make([]string, 0),
		guids:   make([]string, 0),
		nameSet: make(map[string]struct{}),
		guidSet: make(map[string]struct{}),
	}
	if s.DB == nil {
		return scope, fmt.Errorf("managed pool database unavailable")
	}

	var settings models.BasicSettings
	if err := s.DB.WithContext(ctx).First(&settings).Error; err != nil {
		return scope, fmt.Errorf("load managed pools: %w", err)
	}

	var hostPools []*gzfs.ZPool
	var err error
	if s.listHostPools != nil {
		hostPools, err = s.listHostPools(ctx)
	} else {
		if s.GZFS == nil {
			return scope, fmt.Errorf("zfs client unavailable")
		}
		hostPools, err = s.GZFS.Zpool.List(ctx)
	}
	if err != nil {
		return scope, fmt.Errorf("list pools for managed scope: %w", err)
	}

	hostByName := make(map[string]*gzfs.ZPool, len(hostPools))
	for _, pool := range hostPools {
		if pool == nil {
			continue
		}
		name := strings.TrimSpace(pool.Name)
		if name != "" {
			hostByName[name] = pool
		}
	}

	seenConfigured := make(map[string]struct{}, len(settings.Pools))
	for _, configuredName := range settings.Pools {
		configuredName = strings.TrimSpace(configuredName)
		if configuredName == "" {
			continue
		}
		if _, seen := seenConfigured[configuredName]; seen {
			continue
		}
		seenConfigured[configuredName] = struct{}{}

		pool := hostByName[configuredName]
		if pool == nil {
			continue
		}

		name := strings.TrimSpace(pool.Name)
		if _, seen := scope.nameSet[name]; !seen {
			scope.nameSet[name] = struct{}{}
			scope.names = append(scope.names, name)
			scope.pools = append(scope.pools, pool)
		}

		guid := strings.TrimSpace(pool.PoolGUID)
		if guid == "" {
			continue
		}
		if _, seen := scope.guidSet[guid]; !seen {
			scope.guidSet[guid] = struct{}{}
			scope.guids = append(scope.guids, guid)
		}
	}

	return scope, nil
}

func (scope managedPoolScope) containsGUID(guid string) bool {
	_, exists := scope.guidSet[strings.TrimSpace(guid)]
	return exists
}

func (scope managedPoolScope) allowsHistoricalPool(guid, name string) bool {
	guid = strings.TrimSpace(guid)
	if guid != "" {
		_, exists := scope.guidSet[guid]
		return exists
	}
	_, exists := scope.nameSet[strings.TrimSpace(name)]
	return exists
}

func (scope managedPoolScope) filterHistoricalPools(query *gorm.DB) *gorm.DB {
	switch {
	case len(scope.guids) > 0 && len(scope.names) > 0:
		return query.Where("guid IN ? OR (guid = ? AND name IN ?)", scope.guids, "", scope.names)
	case len(scope.guids) > 0:
		return query.Where("guid IN ?", scope.guids)
	case len(scope.names) > 0:
		return query.Where("guid = ? AND name IN ?", "", scope.names)
	default:
		return query.Where("1 = 0")
	}
}

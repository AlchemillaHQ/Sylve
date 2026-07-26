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
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
)

const (
	PoolHealthNone    = "NONE"
	PoolHealthUnknown = "UNKNOWN"
)

// GetPoolHealth returns the most severe state among configured pools.
func (s *Service) GetPoolHealth(ctx context.Context) (string, error) {
	var settings models.BasicSettings
	if err := s.DB.First(&settings).Error; err != nil {
		return "", err
	}

	configured := 0
	for _, name := range settings.Pools {
		if strings.TrimSpace(name) != "" {
			configured++
		}
	}
	if configured == 0 {
		return PoolHealthNone, nil
	}

	pools, err := s.GetUsablePools(ctx)
	if err != nil {
		return "", err
	}

	states := make([]string, 0, len(pools))
	for _, pool := range pools {
		if pool != nil {
			states = append(states, string(pool.State))
		}
	}

	return aggregatePoolHealth(states, len(pools) < configured), nil
}

func aggregatePoolHealth(states []string, missing bool) string {
	worstState := PoolHealthUnknown
	worstRank := 0
	if missing {
		worstRank = poolHealthRank(PoolHealthUnknown)
	}

	for _, state := range states {
		state = strings.ToUpper(strings.TrimSpace(state))
		rank := poolHealthRank(state)
		if rank > worstRank {
			worstState = state
			worstRank = rank
		}
	}

	if worstRank == 0 {
		return PoolHealthUnknown
	}
	return worstState
}

func poolHealthRank(state string) int {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case string(gzfs.ZPoolStateOnline):
		return 1
	case PoolHealthUnknown:
		return 2
	case string(gzfs.ZPoolStateDegraded):
		return 3
	case string(gzfs.ZPoolStateOffline), string(gzfs.ZPoolStateRemoved):
		return 4
	case string(gzfs.ZPoolStateFaulted), string(gzfs.ZPoolStateUnavailible), string(gzfs.ZPoolStateCorruptData), "SUSPENDED":
		return 5
	default:
		return 2
	}
}

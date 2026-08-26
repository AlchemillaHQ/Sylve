// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
)

const netlinkInvalidationFlushInterval = time.Second

func (s *Service) consumeZFSEvents(ctx context.Context, events <-chan *zfsEvent, flushInterval time.Duration) {
	pending := make(map[string]map[string]struct{}, 2)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.flushZFSCacheInvalidations(context.Background(), pending)
			logger.L.Debug().Msg("Stopped Netlink consumer loop")
			return
		case ev := <-events:
			kind, invalidate := zfsCacheInvalidationKind(ev)
			pool, hasPool := zfsEventPool(ev)
			if invalidate && hasPool {
				if pending[kind] == nil {
					pending[kind] = make(map[string]struct{})
				}
				pending[kind][pool] = struct{}{}
			}
			if shouldHandleZFSStateChangeEvent(ev) {
				s.emitPoolStateNotification(ctx, ev)
			}
		case <-ticker.C:
			s.flushZFSCacheInvalidations(ctx, pending)
		}
	}
}

func (s *Service) flushZFSCacheInvalidations(ctx context.Context, pending map[string]map[string]struct{}) {
	if len(pending) == 0 {
		return
	}

	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	var settings models.BasicSettings
	err := s.DB.WithContext(readCtx).First(&settings).Error
	cancel()
	if err != nil {
		logger.L.Error().Err(err).Msg("Failed to load allowed pools for ZFS cache invalidation")
		return
	}

	allowedPools := make(map[string]struct{}, len(settings.Pools))
	for _, pool := range settings.Pools {
		allowedPools[pool] = struct{}{}
	}

	for kind, pools := range pending {
		allowed := false
		for pool := range pools {
			if _, ok := allowedPools[pool]; ok {
				allowed = true
				break
			}
		}
		if !allowed {
			delete(pending, kind)
			continue
		}

		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = db.InvalidateZFSCache(s.DB.WithContext(writeCtx), kind)
		cancel()
		if err != nil {
			logger.L.Error().Err(err).Str("kind", kind).Msg("Failed to persist ZFS cache invalidation")
			continue
		}

		delete(pending, kind)
	}
}

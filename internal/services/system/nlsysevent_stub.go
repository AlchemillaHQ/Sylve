// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build !freebsd

package system

import (
	"context"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
)

// StartNetlinkWatcher is a cross-platform stub.
// It gracefully blocks until context cancellation so the service
// remains stable during local development or non-FreeBSD testing.
func (s *Service) StartNetlinkWatcher(ctx context.Context) {
	go func() {
		logger.L.Info().Msg("Starting mock Netlink ZFS watcher (Stub Mode)...")
		<-ctx.Done()
		logger.L.Debug().Msg("Stopped mock Netlink ZFS watcher")
	}()
}

// NetlinkEventsCleaner retains the exact DB cleanup logic,
// allowing you to test GORM operations cross-platform.
func (s *Service) NetlinkEventsCleaner(ctx context.Context) {
	cleanup := func() {
		cutoff := time.Now().Add(-24 * time.Hour)

		res := s.DB.Unscoped().
			Where("created_at < ?", cutoff).
			Delete(&models.NetlinkEvent{})

		if res.Error != nil {
			logger.L.Error().
				Err(res.Error).
				Msg("netlink cleanup failed")
		} else if res.RowsAffected > 0 {
			logger.L.Debug().Int64("count", res.RowsAffected).Msg("Pruned old ZFS events")
		}
	}

	go func() {
		cleanup()

		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.L.Debug().Msg("Stopped Netlink events cleaner")
				return
			case <-ticker.C:
				cleanup()
			}
		}
	}()
}

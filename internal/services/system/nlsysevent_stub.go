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

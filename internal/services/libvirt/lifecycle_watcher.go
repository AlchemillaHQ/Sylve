// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/logger"
)

const lifecycleWatcherRetryDelay = 2 * time.Second

func (s *Service) StartLifecycleWatcher(ctx context.Context) {
	go func() {
		for {
			if ctx.Err() != nil {
				logger.L.Debug().Msg("Stopped libvirt lifecycle watcher")
				return
			}

			conn, err := s.ensureConnection()
			if err != nil {
				logger.L.Error().Err(err).Msg("Failed to establish libvirt connection for lifecycle watcher")

				select {
				case <-ctx.Done():
					logger.L.Debug().Msg("Stopped libvirt lifecycle watcher")
					return
				case <-time.After(lifecycleWatcherRetryDelay):
					continue
				}
			}

			events, err := conn.LifecycleEvents(ctx)
			if err != nil {
				logger.L.Error().Err(err).Msg("Failed to subscribe to libvirt lifecycle events")

				select {
				case <-ctx.Done():
					logger.L.Debug().Msg("Stopped libvirt lifecycle watcher")
					return
				case <-time.After(lifecycleWatcherRetryDelay):
					continue
				}
			}

			logger.L.Info().Msg("Subscribed to libvirt lifecycle events")

			streamClosed := false
			for !streamClosed {
				select {
				case <-ctx.Done():
					logger.L.Debug().Msg("Stopped libvirt lifecycle watcher")
					return
				case ev, ok := <-events:
					if !ok {
						streamClosed = true
						break
					}

					domainName := strings.TrimSpace(ev.Dom.Name)
					reason := fmt.Sprintf(
						"vm_lifecycle_%s_event_%d_detail_%d",
						domainName,
						ev.Event,
						ev.Detail,
					)
					s.emitLeftPanelRefresh(reason)
				}
			}

			logger.L.Warn().Msg("Libvirt lifecycle event stream closed; retrying")

			select {
			case <-ctx.Done():
				logger.L.Debug().Msg("Stopped libvirt lifecycle watcher")
				return
			case <-time.After(lifecycleWatcherRetryDelay):
			}
		}
	}()
}

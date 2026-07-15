// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package system

/*
extern int start_netlink_watcher(void);
*/
import "C"
import (
	"context"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
)

var zfsEventsChan = make(chan *models.NetlinkEvent, 1024)

//export onZFSEvent
func onZFSEvent(cSystem, cSubsystem, cType, cData *C.char) {
	system := C.GoString(cSystem)
	subsystem := C.GoString(cSubsystem)
	evType := C.GoString(cType)
	data := C.GoString(cData)

	ev := &models.NetlinkEvent{
		System:    system,
		Subsystem: subsystem,
		Type:      evType,
		Attrs:     make(map[string]string),
		Raw:       "!system=" + system + " subsystem=" + subsystem + " type=" + evType + " | " + data,
	}

	if shouldLogNetlinkEvent(ev) {
		logger.L.Debug().Str("system", system).
			Str("subsystem", subsystem).
			Str("type", evType).
			Str("data", data).
			Msg("Received ZFS event")
	}

	for _, field := range strings.Fields(data) {
		kv := strings.SplitN(field, "=", 2)
		if len(kv) != 2 {
			continue
		}

		key := kv[0]
		val := strings.Trim(kv[1], "\"")
		ev.Attrs[key] = val
	}

	select {
	case zfsEventsChan <- ev:
	default:
		// logger.L.Warn().Msg("ZFS event dropped, consumer channel full")
	}
}

func (s *Service) StartNetlinkWatcher(ctx context.Context) {
	go func() {
		logger.L.Info().Msg("Starting FreeBSD Netlink ZFS watcher...")
		res := C.start_netlink_watcher()
		if res == -99 {
			logger.L.Warn().Msg("Netlink ZFS watcher not supported on this FreeBSD version (requires FreeBSD 15+)")
			return
		}
		if res != 0 {
			logger.L.Error().Int("code", int(res)).Msg("Netlink watcher exited with error")
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				logger.L.Debug().Msg("Stopped Netlink consumer loop")
				return
			case ev := <-zfsEventsChan:
				if !shouldPersistNetlinkEvent(ev) {
					continue
				}

				if err := s.DB.Create(ev).Error; err != nil {
					logger.L.Error().Err(err).Msg("Failed to insert Netlink ZFS event")
				}
				s.emitPoolStateNotification(ctx, ev)
			}
		}
	}()
}

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

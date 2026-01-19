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
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
	"golang.org/x/sys/unix"
)

func parseEvent(line string) (*models.DevdEvent, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "!") {
		return nil, false
	}

	ev := &models.DevdEvent{
		Attrs: make(map[string]string),
		Raw:   line,
	}

	meta := line[1:]

	for _, field := range strings.Fields(meta) {
		kv := strings.SplitN(field, "=", 2)
		if len(kv) != 2 {
			continue
		}

		key := kv[0]
		val := strings.Trim(kv[1], "\"")

		ev.Attrs[key] = val

		switch key {
		case "system":
			ev.System = val
		case "subsystem":
			ev.Subsystem = val
		case "type":
			ev.Type = val
		}
	}

	return ev, true
}

func (s *Service) StartDevdParser(
	ctx context.Context,
) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				logger.L.Debug().Msg("Stopped devd parser")
				return
			default:
			}

			fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_SEQPACKET, 0)
			if err != nil {
				time.Sleep(time.Second)
				continue
			}

			if err := unix.Connect(fd, &unix.SockaddrUnix{
				Name: "/var/run/devd.seqpacket.pipe",
			}); err != nil {
				unix.Close(fd)
				time.Sleep(time.Second)
				continue
			}

			buf := make([]byte, 8192)

			for {
				select {
				case <-ctx.Done():
					unix.Close(fd)
					return
				default:
				}

				n, _, err := unix.Recvfrom(fd, buf, 0)
				if err != nil {
					// devd restarted or socket broke
					unix.Close(fd)
					break
				}

				msg := string(buf[:n])

				for _, line := range strings.Split(msg, "\n") {
					ev, ok := parseEvent(line)
					if !ok {
						continue
					}

					if ev.System == "" || ev.Subsystem == "" {
						continue
					}

					record := &models.DevdEvent{
						System:    ev.System,
						Subsystem: ev.Subsystem,
						Type:      ev.Type,
						Attrs:     ev.Attrs,
						Raw:       ev.Raw,
					}

					_ = s.DB.Create(record).Error
				}
			}

			time.Sleep(time.Second)
		}
	}()
}

func (s *Service) DevdEventsCleaner(ctx context.Context) {
	cleanup := func() {
		cutoff := time.Now().Add(-24 * time.Hour)

		res := s.DB.
			Where("created_at < ?", cutoff).
			Delete(&models.DevdEvent{})

		if res.Error != nil {
			logger.L.Error().
				Err(res.Error).
				Msg("devd_events cleanup failed")
		}
	}

	go func() {
		cleanup()

		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanup()
			}
		}
	}()
}

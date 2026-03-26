// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package info

import (
	"context"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

const netRetention = 2 * time.Hour
const auditRetentionInterval = 6 * time.Hour
const netSampleInterval = 2 * time.Minute

func (s *Service) StoreStats() {
	now := time.Now()

	if c, err := s.GetCPUInfo(true); err == nil {
		s.cpuDB().Create(&infoModels.CPU{Usage: c.Usage})
	} else {
		logger.L.Err(err).Msg("Failed to get CPU stats")
	}

	if r, err := s.GetRAMInfo(); err == nil {
		s.DB.Create(&infoModels.RAM{Usage: r.UsedPercent})
	} else {
		logger.L.Err(err).Msg("Failed to get RAM stats")
	}

	if sw, err := s.GetSwapInfo(); err == nil {
		s.DB.Create(&infoModels.Swap{Usage: sw.UsedPercent})
	} else {
		logger.L.Err(err).Msg("Failed to get Swap stats")
	}

	pruneGFS(s.cpuDB(), now, infoModels.CPU{})
	pruneGFS(s.DB, now, infoModels.RAM{})
	pruneGFS(s.DB, now, infoModels.Swap{})
}

func pruneGFS[T db.TimeSeriesRow](dbConn *gorm.DB, now time.Time, dummy T) {
	var rows []T
	err := dbConn.Model(&dummy).
		Select("id", "created_at").
		Order("created_at desc").
		Find(&rows).Error

	if err != nil {
		logger.L.Err(err).Msgf("failed loading rows for prune: %T", dummy)
		return
	}

	_, deleteIDs := db.ApplyGFS(now, rows)
	if len(deleteIDs) == 0 {
		return
	}

	if err := dbConn.Unscoped().Delete(&dummy, deleteIDs).Error; err != nil {
		logger.L.Err(err).Msgf("failed pruning stats: %T", dummy)
	}
}

func (s *Service) StoreNetworkInterfaceStats() {
	interfaces, err := s.GetNetworkInterfacesInfo()
	if err != nil || len(interfaces) == 0 {
		if err != nil {
			logger.L.Err(err).Msg("failed to get network interfaces info")
		}
		return
	}

	now := time.Now()
	rowsByKey := make(map[string]infoModels.NetworkInterface, len(interfaces))
	for _, iface := range interfaces {
		key := iface.Name + "|" + iface.Network
		row, exists := rowsByKey[key]
		if !exists {
			rowsByKey[key] = infoModels.NetworkInterface{
				CreatedAt:       now,
				Name:            iface.Name,
				Flags:           iface.Flags,
				Network:         iface.Network,
				Address:         iface.Address,
				IsDelta:         false,
				ReceivedPackets: iface.ReceivedPackets,
				ReceivedErrors:  iface.ReceivedErrors,
				DroppedPackets:  iface.DroppedPackets,
				ReceivedBytes:   iface.ReceivedBytes,
				SentPackets:     iface.SentPackets,
				SendErrors:      iface.SendErrors,
				SentBytes:       iface.SentBytes,
				Collisions:      iface.Collisions,
			}
			continue
		}

		// netstat can emit duplicate rows per interface key; keep max counters to avoid duplicate inflation
		if iface.ReceivedPackets > row.ReceivedPackets {
			row.ReceivedPackets = iface.ReceivedPackets
		}
		if iface.ReceivedErrors > row.ReceivedErrors {
			row.ReceivedErrors = iface.ReceivedErrors
		}
		if iface.DroppedPackets > row.DroppedPackets {
			row.DroppedPackets = iface.DroppedPackets
		}
		if iface.ReceivedBytes > row.ReceivedBytes {
			row.ReceivedBytes = iface.ReceivedBytes
		}
		if iface.SentPackets > row.SentPackets {
			row.SentPackets = iface.SentPackets
		}
		if iface.SendErrors > row.SendErrors {
			row.SendErrors = iface.SendErrors
		}
		if iface.SentBytes > row.SentBytes {
			row.SentBytes = iface.SentBytes
		}
		if iface.Collisions > row.Collisions {
			row.Collisions = iface.Collisions
		}
		rowsByKey[key] = row
	}

	rows := make([]infoModels.NetworkInterface, 0, len(rowsByKey))
	for _, row := range rowsByKey {
		rows = append(rows, row)
	}

	if len(rows) == 0 {
		return
	}

	if err := s.DB.Create(&rows).Error; err != nil {
		logger.L.Err(err).Msg("failed storing network interface stats")
		return
	}

	cutoffPrune := now.Add(-netRetention)
	if err := s.DB.Unscoped().
		Where("created_at < ?", cutoffPrune).
		Delete(&infoModels.NetworkInterface{}).
		Error; err != nil {
		logger.L.Err(err).Msg("failed pruning old network interface stats")
	}
}

func (s *Service) Cron(ctx context.Context) {
	s.StoreStats()
	s.StoreNetworkInterfaceStats()
	s.PruneAuditRecords(time.Now())

	statsTicker := time.NewTicker(10 * time.Second)
	netTicker := time.NewTicker(netSampleInterval)
	auditRetentionTicker := time.NewTicker(auditRetentionInterval)
	defer statsTicker.Stop()
	defer netTicker.Stop()
	defer auditRetentionTicker.Stop()

	logger.L.Info().Msg("Info service cron workers started")

	for {
		select {
		case <-ctx.Done():
			logger.L.Info().Msg("Shutting down info service cron workers")
			return

		case <-statsTicker.C:
			s.StoreStats()

		case <-netTicker.C:
			s.StoreNetworkInterfaceStats()

		case <-auditRetentionTicker.C:
			s.PruneAuditRecords(time.Now())
		}
	}
}

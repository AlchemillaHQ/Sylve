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

func (s *Service) StoreStats() {
	now := time.Now()

	if c, err := s.GetCPUInfo(true); err == nil {
		s.DB.Create(&infoModels.CPU{Usage: c.Usage})
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

	pruneGFS(s.DB, now, infoModels.CPU{})
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
	var recentRows []infoModels.NetworkInterface

	cutoffSearch := now.Add(-5 * time.Minute)
	err = s.DB.Where("created_at >= ?", cutoffSearch).
		Order("created_at DESC").
		Find(&recentRows).Error

	if err != nil {
		logger.L.Err(err).Msg("failed loading previous network interface stats")
		return
	}

	last := make(map[string]infoModels.NetworkInterface)
	for _, r := range recentRows {
		key := r.Name + "|" + r.Network
		if _, exists := last[key]; !exists {
			last[key] = r
		}
	}

	rows := make([]infoModels.NetworkInterface, 0, len(interfaces))

	delta := func(cur, old int64) int64 {
		if cur < old {
			return 0
		}
		return cur - old
	}

	for _, iface := range interfaces {
		key := iface.Name + "|" + iface.Network
		prev, exists := last[key]

		if !exists {
			rows = append(rows, infoModels.NetworkInterface{
				Name:            iface.Name,
				Flags:           iface.Flags,
				Network:         iface.Network,
				Address:         iface.Address,
				IsDelta:         false,
				ReceivedPackets: iface.ReceivedPackets,
				ReceivedBytes:   iface.ReceivedBytes,
				SentPackets:     iface.SentPackets,
				SentBytes:       iface.SentBytes,
			})
			continue
		}

		rows = append(rows, infoModels.NetworkInterface{
			Name:    iface.Name,
			Flags:   iface.Flags,
			Network: iface.Network,
			Address: iface.Address,
			IsDelta: true,

			ReceivedPackets: delta(iface.ReceivedPackets, prev.ReceivedPackets),
			ReceivedErrors:  delta(iface.ReceivedErrors, prev.ReceivedErrors),
			DroppedPackets:  delta(iface.DroppedPackets, prev.DroppedPackets),
			ReceivedBytes:   delta(iface.ReceivedBytes, prev.ReceivedBytes),

			SentPackets: delta(iface.SentPackets, prev.SentPackets),
			SendErrors:  delta(iface.SendErrors, prev.SendErrors),
			SentBytes:   delta(iface.SentBytes, prev.SentBytes),

			Collisions: delta(iface.Collisions, prev.Collisions),
		})
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
		Where("is_delta = true AND created_at < ?", cutoffPrune).
		Delete(&infoModels.NetworkInterface{}).
		Error; err != nil {
		logger.L.Err(err).Msg("failed pruning old network interface deltas")
	}
}

func (s *Service) Cron(ctx context.Context) {
	s.StoreStats()
	s.StoreNetworkInterfaceStats()

	statsTicker := time.NewTicker(10 * time.Second)
	netTicker := time.NewTicker(2 * time.Minute)
	defer statsTicker.Stop()
	defer netTicker.Stop()

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
		}
	}
}

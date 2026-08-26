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

const auditRetentionInterval = 6 * time.Hour

func (s *Service) StoreStats() {
	var cpuRow *infoModels.CPU
	if c, err := s.GetCPUInfo(true); err == nil {
		cpuRow = &infoModels.CPU{Usage: c.Usage}
	} else {
		logger.L.Err(err).Msg("Failed to get CPU stats")
	}

	var ramRow *infoModels.RAM
	if r, err := s.GetRAMInfo(); err == nil {
		ramRow = &infoModels.RAM{Usage: r.UsedPercent}
	} else {
		logger.L.Err(err).Msg("Failed to get RAM stats")
	}

	var swapRow *infoModels.Swap
	if sw, err := s.GetSwapInfo(); err == nil {
		swapRow = &infoModels.Swap{Usage: sw.UsedPercent}
	} else {
		logger.L.Err(err).Msg("Failed to get Swap stats")
	}

	if cpuRow == nil && ramRow == nil && swapRow == nil {
		return
	}

	if err := s.telemetryDB().Transaction(func(tx *gorm.DB) error {
		if cpuRow != nil {
			if err := tx.Create(cpuRow).Error; err != nil {
				return err
			}
		}
		if ramRow != nil {
			if err := tx.Create(ramRow).Error; err != nil {
				return err
			}
		}
		if swapRow != nil {
			if err := tx.Create(swapRow).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		logger.L.Err(err).Msg("Failed to store system stats")
	}
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
	s.netMu.Lock()

	deduped := make(map[string]struct {
		receivedBytes int64
		sentBytes     int64
	}, len(interfaces))

	for _, iface := range interfaces {
		key := iface.Name + "|" + iface.Network
		cur, exists := deduped[key]
		if !exists || iface.ReceivedBytes > cur.receivedBytes {
			cur.receivedBytes = iface.ReceivedBytes
		}
		if !exists || iface.SentBytes > cur.sentBytes {
			cur.sentBytes = iface.SentBytes
		}
		deduped[key] = cur
	}

	if s.lastNet == nil {
		s.lastNet = make(map[string]netCounter, len(deduped))
		for key, cur := range deduped {
			s.lastNet[key] = netCounter{receivedBytes: cur.receivedBytes, sentBytes: cur.sentBytes}
		}
		s.netMu.Unlock()
		return
	}

	var totalReceivedDelta int64
	var totalSentDelta int64

	for key, cur := range deduped {
		prev, ok := s.lastNet[key]
		if !ok {
			s.lastNet[key] = netCounter{receivedBytes: cur.receivedBytes, sentBytes: cur.sentBytes}
			continue
		}

		receivedDelta := int64(0)
		if cur.receivedBytes > prev.receivedBytes {
			receivedDelta = cur.receivedBytes - prev.receivedBytes
		}

		sentDelta := int64(0)
		if cur.sentBytes > prev.sentBytes {
			sentDelta = cur.sentBytes - prev.sentBytes
		}

		totalReceivedDelta += receivedDelta
		totalSentDelta += sentDelta
		s.lastNet[key] = netCounter{receivedBytes: cur.receivedBytes, sentBytes: cur.sentBytes}
	}

	s.netMu.Unlock()

	if !s.lastNetSampleTime.IsZero() {
		elapsed := now.Sub(s.lastNetSampleTime).Seconds()
		if elapsed > 0 {
			totalReceivedDelta = int64(float64(totalReceivedDelta) / elapsed)
			totalSentDelta = int64(float64(totalSentDelta) / elapsed)
		}
	}
	s.lastNetSampleTime = now

	if err := s.networkDB().Create(&infoModels.NetworkInterface{
		ReceivedBytes: totalReceivedDelta,
		SentBytes:     totalSentDelta,
		IsDelta:       true,
	}).Error; err != nil {
		logger.L.Err(err).Msg("failed storing network interface stats")
		return
	}
}

func (s *Service) PruneStats() {
	now := time.Now()
	pruneGFS(s.cpuDB(), now, infoModels.CPU{})
	pruneGFS(s.ramDB(), now, infoModels.RAM{})
	pruneGFS(s.swapDB(), now, infoModels.Swap{})
	pruneGFS(s.networkDB(), now, infoModels.NetworkInterface{})
}

func (s *Service) Cron(ctx context.Context) {
	s.StoreStats()
	s.StoreNetworkInterfaceStats()
	s.PruneStats()
	s.PruneAuditRecords(time.Now())

	statsTicker := time.NewTicker(10 * time.Second)
	pruneTicker := time.NewTicker(5 * time.Minute)
	auditRetentionTicker := time.NewTicker(auditRetentionInterval)
	defer statsTicker.Stop()
	defer pruneTicker.Stop()
	defer auditRetentionTicker.Stop()

	logger.L.Info().Msg("Info service cron workers started")

	for {
		select {
		case <-ctx.Done():
			logger.L.Info().Msg("Shutting down info service cron workers")
			return

		case <-statsTicker.C:
			s.StoreStats()
			s.StoreNetworkInterfaceStats()

		case <-pruneTicker.C:
			s.PruneStats()

		case <-auditRetentionTicker.C:
			s.PruneAuditRecords(time.Now())
		}
	}
}

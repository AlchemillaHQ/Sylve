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
	"fmt"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	"gorm.io/gorm"
)

// GetSummaryHistory returns the chart history used by /[node]/summary. When
// after is non-nil, only rows newer than each per-series cursor are returned.
func (s *Service) GetSummaryHistory(
	ctx context.Context,
	after *infoServiceInterfaces.SummaryHistoryCursors,
) (infoServiceInterfaces.SummaryHistory, error) {
	result := infoServiceInterfaces.SummaryHistory{
		CPU:     make([]infoModels.CPU, 0),
		RAM:     make([]infoModels.RAM, 0),
		Network: make([]infoServiceInterfaces.SummaryHistoryNetworkPoint, 0),
	}
	if after != nil {
		result.Cursors = *after
	}

	err := s.telemetryDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cpuQuery := tx.Order("id ASC")
		if after != nil {
			cpuQuery = cpuQuery.Where("id > ?", after.CPU)
		}
		if err := cpuQuery.Find(&result.CPU).Error; err != nil {
			return fmt.Errorf("load CPU summary history: %w", err)
		}

		ramQuery := tx.Order("id ASC")
		if after != nil {
			ramQuery = ramQuery.Where("id > ?", after.RAM)
		}
		if err := ramQuery.Find(&result.RAM).Error; err != nil {
			return fmt.Errorf("load RAM summary history: %w", err)
		}

		networkQuery := tx.
			Model(&infoModels.NetworkInterface{}).
			Select("id", "received_bytes", "sent_bytes", "created_at").
			Where("is_delta = ?", true).
			Order("id ASC")
		if after != nil {
			networkQuery = networkQuery.Where("id > ?", after.Network)
		}
		if err := networkQuery.Scan(&result.Network).Error; err != nil {
			return fmt.Errorf("load network summary history: %w", err)
		}

		return nil
	})
	if err != nil {
		return infoServiceInterfaces.SummaryHistory{}, err
	}

	for i := range result.CPU {
		if result.CPU[i].ID > result.Cursors.CPU {
			result.Cursors.CPU = result.CPU[i].ID
		}
	}
	for i := range result.RAM {
		if result.RAM[i].ID > result.Cursors.RAM {
			result.Cursors.RAM = result.RAM[i].ID
		}
	}
	for i := range result.Network {
		if result.Network[i].ID > result.Cursors.Network {
			result.Cursors.Network = result.Network[i].ID
		}
	}

	return result, nil
}

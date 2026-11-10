// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package info

import (
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/logger"
)

func (s *Service) GetAuditRecords(limit int, userID *uint) ([]infoModels.AuditRecord, error) {
	query := s.auditDB().Order("created_at desc").Limit(limit)
	if userID != nil {
		query = query.Where("user_id = ?", *userID)
	}
	var records []infoModels.AuditRecord
	err := query.Find(&records).Error

	return records, err
}

func (s *Service) PruneAuditRecords(now time.Time) {
	if err := db.EnforceAuditRecordRetention(s.auditDB(), now); err != nil {
		logger.L.Error().Err(err).Msg("failed to apply audit records retention")
	}
}

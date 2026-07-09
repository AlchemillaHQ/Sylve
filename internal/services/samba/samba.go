// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"context"
	"sync"
	"time"

	"github.com/alchemillahq/gzfs"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	sambaServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/samba"
	"github.com/alchemillahq/sylve/internal/logger"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"

	"gorm.io/gorm"
)

var _ sambaServiceInterfaces.SambaServiceInterface = (*Service)(nil)

type Service struct {
	DB              *gorm.DB
	TelemetryDB     *gorm.DB
	ZFS             zfsServiceInterfaces.ZfsServiceInterface
	GZFS            *gzfs.Client
	OnConfigChange  func() error

	auditFileOffset int64
	auditFileMu     sync.Mutex
	recentMkdirs    map[string]time.Time
	auditInsertCh   chan []sambaModels.SambaAuditLog
}

func NewSambaService(
	db *gorm.DB,
	telemetryDB *gorm.DB,
	zfs zfsServiceInterfaces.ZfsServiceInterface,
	gzfs *gzfs.Client,
) sambaServiceInterfaces.SambaServiceInterface {
	return &Service{
		DB:            db,
		TelemetryDB:   telemetryDB,
		ZFS:           zfs,
		GZFS:          gzfs,
		recentMkdirs:  make(map[string]time.Time),
		auditInsertCh: make(chan []sambaModels.SambaAuditLog, 64),
	}
}

func (s *Service) auditDB() *gorm.DB {
	if s.TelemetryDB != nil {
		return s.TelemetryDB
	}

	return s.DB
}

func (s *Service) auditBatchWriter(ctx context.Context) {
	auditDB := s.auditDB()
	for {
		select {
		case <-ctx.Done():
			return
		case batch := <-s.auditInsertCh:
			if err := auditDB.CreateInBatches(&batch, len(batch)).Error; err != nil {
				logger.L.Error().Err(err).Msg("failed to insert audit log batch")
			}
		}
	}
}

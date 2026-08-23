// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"os"
	"sync"
	"time"

	"github.com/alchemillahq/gzfs"
	sambaServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/samba"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"

	"gorm.io/gorm"
)

var _ sambaServiceInterfaces.SambaServiceInterface = (*Service)(nil)

type Service struct {
	DB                      *gorm.DB
	TelemetryDB             *gorm.DB
	ZFS                     zfsServiceInterfaces.ZfsServiceInterface
	GZFS                    *gzfs.Client
	OnConfigChange          func() error
	EnsureMdnsEnabled       func(*gorm.DB) error
	WithServiceSettingsLock func(func() error) error

	auditFileOffset int64
	auditFileMu     sync.Mutex
	auditFile       *os.File
	lastAuditLogID  int
	recentMkdirs    map[string]time.Time
}

func NewSambaService(
	db *gorm.DB,
	telemetryDB *gorm.DB,
	zfs zfsServiceInterfaces.ZfsServiceInterface,
	gzfs *gzfs.Client,
) sambaServiceInterfaces.SambaServiceInterface {
	return &Service{
		DB:           db,
		TelemetryDB:  telemetryDB,
		ZFS:          zfs,
		GZFS:         gzfs,
		recentMkdirs: make(map[string]time.Time),
	}
}

func (s *Service) auditDB() *gorm.DB {
	if s.TelemetryDB != nil {
		return s.TelemetryDB
	}

	return s.DB
}

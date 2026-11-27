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
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alchemillahq/gzfs"
	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	sambaServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/samba"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/network/interfaceref"
	"github.com/alchemillahq/sylve/internal/services/directoryservices"

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
	Directory               *directoryservices.Service
	directoryOverride       *directoryModels.ActiveDirectory

	interfaceReferenceCoordinator *interfaceref.Coordinator

	auditFileOffset     int64
	auditFileMu         sync.Mutex
	auditFile           *os.File
	lastAuditLogID      int
	recentMkdirs        map[string]time.Time
	auditCursorLoaded   bool
	auditWatcherStarted atomic.Bool
	permissionJobsOnce  sync.Once
	permissionJobs      *permissionJobs
}

var ErrRequiredByDirectory = errors.New("samba_required_by_active_directory")

func (s *Service) StartAuditWatcher(ctx context.Context) {
	if s.auditWatcherStarted.CompareAndSwap(false, true) {
		go s.watchAuditLogs(ctx)
	}
}

func (s *Service) EnsureCanStop() error {
	ad, err := s.directorySettings()
	if err != nil {
		return fmt.Errorf("failed_to_read_directory_settings: %w", err)
	}
	if ad.Enabled || ad.Joined || ad.MembershipPending != "" {
		return ErrRequiredByDirectory
	}
	return nil
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

func (s *Service) SetInterfaceReferenceCoordinator(coordinator *interfaceref.Coordinator) {
	s.interfaceReferenceCoordinator = coordinator
}

func (s *Service) datasetByGUID(ctx context.Context, guid string) (*gzfs.Dataset, error) {
	if s.ZFS != nil {
		return s.ZFS.DatasetByGUID(ctx, guid)
	}
	return s.GZFS.ZFS.GetByGUID(ctx, guid, false)
}

func (s *Service) lockInterfaceReferencesRead() func() {
	return s.interfaceReferenceCoordinator.ReadLock()
}

func (s *Service) auditDB() *gorm.DB {
	if s.TelemetryDB != nil {
		return s.TelemetryDB
	}

	return s.DB
}

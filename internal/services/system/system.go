// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"sync"
	"time"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	sysctl "github.com/alchemillahq/sylve/pkg/utils/sysctl"

	"github.com/alchemillahq/gzfs"
	"gorm.io/gorm"
)

var _ systemServiceInterfaces.SystemServiceInterface = (*Service)(nil)

type Service struct {
	DB          *gorm.DB
	initMutex   sync.Mutex
	syncMutex   sync.Mutex
	achMutex    sync.Mutex
	GZFS        *gzfs.Client
	DiskService diskServiceInterfaces.DiskServiceInterface

	tunMutex    sync.Mutex
	tunCache    []sysctl.Tunable
	tunCachedAt time.Time

	MdnsRebuild func() error
}

func NewSystemService(db *gorm.DB, gzfs *gzfs.Client) systemServiceInterfaces.SystemServiceInterface {
	return &Service{
		DB:   db,
		GZFS: gzfs,
	}
}

func (s *Service) SetDiskService(ds diskServiceInterfaces.DiskServiceInterface) {
	s.DiskService = ds
}

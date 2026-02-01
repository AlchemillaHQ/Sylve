// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"net/url"
	"slices"
	"sync"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/logger"

	"github.com/digitalocean/go-libvirt"
	"gorm.io/gorm"
)

var _ libvirtServiceInterfaces.LibvirtServiceInterface = (*Service)(nil)

type Service struct {
	DB *gorm.DB

	System systemServiceInterfaces.SystemServiceInterface

	Conn *libvirt.Libvirt

	actionMutex sync.Mutex
	crudMutex   sync.Mutex

	GZFS *gzfs.Client
}

func NewLibvirtService(db *gorm.DB, system systemServiceInterfaces.SystemServiceInterface, gzfs *gzfs.Client) libvirtServiceInterfaces.LibvirtServiceInterface {
	skeleton := &Service{
		DB:     db,
		System: system,
		Conn:   nil,
		GZFS:   gzfs,
	}

	var basicSettings models.BasicSettings

	err := db.First(&basicSettings).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return skeleton
		} else {
			logger.L.Fatal().Err(err).Msg("failed to check basic settings")
		}
	} else {
		if !slices.Contains(basicSettings.Services, models.Virtualization) {
			logger.L.Debug().Msg("Virtualization not enabled, skipping libvirt initialization")
			return skeleton
		}
	}

	uri, _ := url.Parse("bhyve:///system")
	l, err := libvirt.ConnectToURI(uri)
	if err != nil {
		logger.L.Fatal().Err(err).Msg("failed to connect to libvirt")
	}

	v, err := l.ConnectGetLibVersion()

	if err != nil {
		logger.L.Fatal().Err(err).Msg("failed to retrieve libvirt version")
	}

	logger.L.Info().Msgf("Libvirt version: %d", v)

	skeleton.Conn = l

	return skeleton
}

func (s *Service) CheckVersion() error {
	_, err := s.Conn.ConnectGetLibVersion()
	if err != nil {
		return err
	}

	return nil
}

func (s *Service) IsVirtualizationEnabled() bool {
	var basicSettings models.BasicSettings
	err := s.DB.First(&basicSettings).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return false
		} else {
			return false
		}
	} else {
		if !slices.Contains(basicSettings.Services, models.Virtualization) {
			logger.L.Debug().Msg("Virtualization not enabled, skipping libvirt initialization")
			return false
		}
	}

	return true
}

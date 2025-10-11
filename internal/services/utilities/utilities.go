// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/logger"

	"gorm.io/gorm"
)

var _ utilitiesServiceInterfaces.UtilitiesServiceInterface = (*Service)(nil)

type Service struct {
	DB          *gorm.DB
	Aria2Client *Aria2Client
	ISOScanner  *ISOScanner
}

func NewUtilitiesService(db *gorm.DB) utilitiesServiceInterfaces.UtilitiesServiceInterface {
	// Initialize ISO scanner
	isoScanner, err := NewISOScanner()
	if err != nil {
		logger.L.Warn().Msgf("Failed to create ISO scanner: %v", err)
	}

	// Initialize aria2 client
	aria2Client, err := NewAria2Client()
	if err != nil {
		logger.L.Warn().Msgf("Failed to create aria2 client: %v", err)
	}

	return &Service{
		DB:          db,
		Aria2Client: aria2Client,
		ISOScanner:  isoScanner,
	}
}

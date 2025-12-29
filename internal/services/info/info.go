// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package info

import (
	"github.com/alchemillahq/gzfs"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"

	"gorm.io/gorm"
)

var _ infoServiceInterfaces.InfoServiceInterface = (*Service)(nil)

type Service struct {
	DB   *gorm.DB
	GZFS *gzfs.Client
}

func NewInfoService(db *gorm.DB, gzfs *gzfs.Client) infoServiceInterfaces.InfoServiceInterface {
	return &Service{
		DB:   db,
		GZFS: gzfs,
	}
}

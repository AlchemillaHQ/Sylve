// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	sambaServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/samba"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"

	"gorm.io/gorm"
)

var _ sambaServiceInterfaces.SambaServiceInterface = (*Service)(nil)

type Service struct {
	DB  *gorm.DB
	ZFS zfsServiceInterfaces.ZfsServiceInterface
}

func NewSambaService(db *gorm.DB, zfs zfsServiceInterfaces.ZfsServiceInterface) sambaServiceInterfaces.SambaServiceInterface {
	return &Service{
		DB:  db,
		ZFS: zfs,
	}
}

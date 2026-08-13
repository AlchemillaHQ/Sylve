// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsi

import (
	"sync"

	iscsiServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/iscsi"
	"gorm.io/gorm"
)

var _ iscsiServiceInterfaces.ISCSIServiceInterface = (*Service)(nil)

type Service struct {
	DB *gorm.DB

	mutationMu sync.Mutex
}

func NewISCSIService(db *gorm.DB) iscsiServiceInterfaces.ISCSIServiceInterface {
	return &Service{DB: db}
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func newNetworkServiceTestDB(t *testing.T, migrateModels ...any) *gorm.DB {
	return testutil.NewSQLiteTestDB(t, migrateModels...)
}

func newNetworkServiceForTest(t *testing.T, migrateModels ...any) (*Service, *gorm.DB) {
	t.Helper()

	db := newNetworkServiceTestDB(t, migrateModels...)
	return &Service{DB: db}, db
}

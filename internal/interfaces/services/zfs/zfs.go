// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsServiceInterfaces

import infoModels "sylve/internal/db/models/info"

type ZfsServiceInterface interface {
	GetTotalIODelayHisorical() ([]infoModels.IODelay, error)
	CreatePool(Zpool) error

	GetDatasets() ([]Dataset, error)

	CreateSnapshot(guid string, name string, recursive bool) error
	RollbackSnapshot(guid string, destroyMoreRecent bool) error
	DeleteSnapshot(guid string) error

	CreateFilesystem(name string, props map[string]string) error
	DeleteFilesystem(guid string) error

	Cron()
}

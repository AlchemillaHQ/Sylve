// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsServiceInterfaces

import (
	"context"
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/pkg/zfs"
)

type RetentionSnapInfo struct {
	Name    string
	Dataset *zfs.Dataset
	Time    time.Time
}

type ZfsServiceInterface interface {
	GetTotalIODelayHisorical() ([]infoModels.IODelay, error)
	GetZpoolHistoricalStats(intervalMinutes int, limit int) (map[string][]PoolStatPoint, int, error)

	CreatePool(Zpool) error
	DeletePool(poolName string) error

	GetDatasets(t string) ([]*zfs.Dataset, error)
	BulkDeleteDataset(guids []string) error

	CreateSnapshot(guid string, name string, recursive bool) error
	RollbackSnapshot(guid string, destroyMoreRecent bool) error
	DeleteSnapshot(guid string, recursive bool) error

	GetPeriodicSnapshots() ([]zfsModels.PeriodicSnapshot, error)
	AddPeriodicSnapshot(CreatePeriodicSnapshotJobRequest) error
	DeletePeriodicSnapshot(guid string) error
	StartSnapshotScheduler(ctx context.Context)

	CreateFilesystem(name string, props map[string]string) error
	DeleteFilesystem(guid string) error

	SyncLibvirtPools() error

	StoreStats(interval int)
	Cron()
}

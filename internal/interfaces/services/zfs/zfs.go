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

	"github.com/alchemillahq/gzfs"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/pkg/zfs"
)

type RetentionSnapInfo struct {
	Name    string
	Dataset *zfs.Dataset
	Time    time.Time
}

type ZfsServiceInterface interface {
	StoreStats()
	RemoveNonExistentPools()
	Cron()

	CreateFilesystem(ctx context.Context, name string, props map[string]string) error
	EditFilesystem(ctx context.Context, guid string, props map[string]string) error
	DeleteFilesystem(ctx context.Context, guid string) error

	CreateVolume(ctx context.Context, name string, parent string, props map[string]string) error
	EditVolume(ctx context.Context, name string, props map[string]string) error
	DeleteVolume(ctx context.Context, guid string) error
	FlashVolume(ctx context.Context, guid string, uuid string) error

	GetDatasets(ctx context.Context, t string) ([]*gzfs.Dataset, error)
	GetDatasetByGUID(guid string) (*gzfs.Dataset, error)
	GetSnapshotByGUID(guid string) (*gzfs.Dataset, error)
	GetFsOrVolByGUID(guid string) (*gzfs.Dataset, error)
	BulkDeleteDataset(ctx context.Context, guids []string) error
	IsDatasetInUse(guid string, failEarly bool) bool

	GetZpoolHistoricalStats(intervalMinutes int, limit int) (map[string][]PoolStatPoint, int, error)

	CreatePool(Zpool) error
	DeletePool(poolName string) error

	CreateSnapshot(guid string, name string, recursive bool) error
	RollbackSnapshot(guid string, destroyMoreRecent bool) error
	DeleteSnapshot(guid string, recursive bool) error

	GetPeriodicSnapshots() ([]zfsModels.PeriodicSnapshot, error)
	AddPeriodicSnapshot(CreatePeriodicSnapshotJobRequest) error
	DeletePeriodicSnapshot(guid string) error
	StartSnapshotScheduler(ctx context.Context)

	SyncLibvirtPools(ctx context.Context) error
}

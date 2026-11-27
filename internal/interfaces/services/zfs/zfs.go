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
)

type RetentionSnapInfo struct {
	Name    string
	Dataset *gzfs.Dataset
	Time    time.Time
}

type SimpleZFSDiskUsage struct {
	Total float64 `json:"total"`
	Usage float64 `json:"usage"`
}

type EditVolumeRequest struct {
	Properties map[string]string `json:"properties"`
	Inherit    []string          `json:"inherit"`
}

type ZfsServiceInterface interface {
	StoreStats()
	PruneHistoricalStats()
	RemoveNonExistentPools()
	Cron(ctx context.Context)

	CreateFilesystem(ctx context.Context, name string, parent string, props map[string]string) error
	EditFilesystem(ctx context.Context, guid string, props map[string]string, inherit []string) error
	DeleteFilesystem(ctx context.Context, guid string) error

	CreateVolume(ctx context.Context, name string, parent string, props map[string]string) error
	EditVolume(ctx context.Context, guid string, props map[string]string, inherit []string) error
	DeleteVolume(ctx context.Context, guid string) error
	FlashVolume(ctx context.Context, guid string, uuid string) error

	GetDatasets(ctx context.Context, t gzfs.DatasetType) ([]*gzfs.Dataset, error)
	CachedDatasets(ctx context.Context, t gzfs.DatasetType) ([]*gzfs.Dataset, error)
	DatasetByGUID(ctx context.Context, guid string) (*gzfs.Dataset, error)
	GetSnapshotSummaries(ctx context.Context) ([]SnapshotSummary, error)
	BulkDeleteDataset(ctx context.Context, targets []DatasetDeletionTarget) error
	IsDatasetInUse(guid string, failEarly bool) bool

	GetPoolStatus(ctx context.Context, guid string) (*gzfs.ZPoolStatusPool, error)
	ScrubPool(ctx context.Context, guid string) error
	CreatePool(ctx context.Context, req CreateZPoolRequest) error
	EditPool(ctx context.Context, guid string, props map[string]string, spares *[]string) error
	DeletePool(ctx context.Context, guid string) error
	ReplaceDevice(ctx context.Context, guid, old, latest string, options ...PoolDeviceMutationOptions) error
	AttachDevice(ctx context.Context, guid, device, latest string, options ...PoolDeviceMutationOptions) error
	DetachDevice(ctx context.Context, guid, device string, options ...PoolDeviceMutationOptions) error
	GetDashboardSnapshot(ctx context.Context) (DashboardSnapshot, error)
	GetDashboardHistory(ctx context.Context, query DashboardHistoryQuery) (DashboardHistory, error)
	GetDashboardHistoryDelta(ctx context.Context, query DashboardDeltaQuery) (DashboardHistory, error)

	CreateSnapshot(ctx context.Context, guid string, name string, recursive bool) error
	DeleteSnapshot(ctx context.Context, guid, name string, recursive bool) error
	GetPeriodicSnapshots() ([]zfsModels.PeriodicSnapshot, error)
	AddPeriodicSnapshot(ctx context.Context, req CreatePeriodicSnapshotJobRequest) error
	ModifyPeriodicSnapshotRetention(ctx context.Context, id uint, req ModifyPeriodicSnapshotRetentionRequest) error
	DeletePeriodicSnapshot(ctx context.Context, id uint) error
	BulkDeletePeriodicSnapshots(ctx context.Context, ids []uint) error
	StartSnapshotScheduler(ctx context.Context)
	RollbackSnapshot(ctx context.Context, guid, name string, destroyMoreRecent bool, expectedNewer []string) error

	GetUsablePools(ctx context.Context) ([]*gzfs.ZPool, error)
	GetDisksUsage(ctx context.Context) (SimpleZFSDiskUsage, error)
}

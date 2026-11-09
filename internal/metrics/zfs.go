// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package metrics

import (
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type ZFSPool struct {
	Name, GUID, Health                      string
	Size, Allocated, Free                   OptionalValue
	FragmentationRatio, DedupRatio          OptionalValue
	IOExpiresAt                             time.Time
	ReadIOPS, WriteIOPS                     uint64
	ReadBytesPerSecond, WriteBytesPerSecond uint64
	ReadLatencySeconds, WriteLatencySeconds OptionalValue
}

type ZFSDataset struct {
	Name, Pool, GUID, Type                   string
	Used, Available, Referenced, LogicalUsed OptionalValue
	Quota, VolumeSize, CompressionRatio      OptionalValue
}

type ZFSARC struct {
	Size, TargetSize, MinSize, MaxSize uint64
	Hits, Misses                       uint64
}

type ZFSSnapshot struct {
	Pools    Snapshot[ZFSPool]
	Datasets Snapshot[ZFSDataset]
	ARC      Snapshot[ZFSARC]
}

var (
	zfsPoolInfo           = gauge("zfs_pool_info", "Information about an imported Sylve-managed ZFS pool.", "pool", "guid")
	zfsPoolHealth         = gauge("zfs_pool_health", "ZFS pool health (one-hot).", "pool", "state")
	zfsPoolSize           = gauge("zfs_pool_size_bytes", "Total ZFS pool space in bytes.", "pool")
	zfsPoolAllocated      = gauge("zfs_pool_allocated_bytes", "Allocated ZFS pool space in bytes.", "pool")
	zfsPoolFree           = gauge("zfs_pool_free_bytes", "Free ZFS pool space in bytes.", "pool")
	zfsPoolFragmentation  = gauge("zfs_pool_fragmentation_ratio", "ZFS pool fragmentation as a ratio from 0 to 1.", "pool")
	zfsPoolDedup          = gauge("zfs_pool_dedup_ratio", "ZFS pool deduplication ratio.", "pool")
	zfsPoolIOSuccess      = gauge("zfs_pool_io_collection_success", "Whether a ZFS pool I/O sample less than 30 seconds old is available.", "pool")
	zfsPoolReadIOPS       = gauge("zfs_pool_read_operations_per_second", "Logical read operations per second over the latest zpool iostat interval.", "pool")
	zfsPoolWriteIOPS      = gauge("zfs_pool_write_operations_per_second", "Logical write operations per second over the latest zpool iostat interval.", "pool")
	zfsPoolReadBytes      = gauge("zfs_pool_read_bytes_per_second", "Logical read bytes per second over the latest zpool iostat interval.", "pool")
	zfsPoolWriteBytes     = gauge("zfs_pool_write_bytes_per_second", "Logical write bytes per second over the latest zpool iostat interval.", "pool")
	zfsPoolReadLatency    = gauge("zfs_pool_read_latency_seconds", "Average total read wait time, including queueing and device service, over the latest zpool iostat interval.", "pool")
	zfsPoolWriteLatency   = gauge("zfs_pool_write_latency_seconds", "Average total write wait time, including queueing and device service, over the latest zpool iostat interval.", "pool")
	zfsDatasetInfo        = gauge("zfs_dataset_info", "Information about a filesystem or zvol on a Sylve-managed ZFS pool.", "pool", "dataset", "guid", "type")
	zfsDatasetUsed        = gauge("zfs_dataset_used_bytes", "Space consumed by a dataset and its descendants in bytes.", "pool", "dataset")
	zfsDatasetAvailable   = gauge("zfs_dataset_available_bytes", "Space available to a dataset in bytes.", "pool", "dataset")
	zfsDatasetReferenced  = gauge("zfs_dataset_referenced_bytes", "Data accessible by a dataset in bytes.", "pool", "dataset")
	zfsDatasetLogicalUsed = gauge("zfs_dataset_logical_used_bytes", "Logical space consumed by a dataset and its descendants, ignoring compression and copies, in bytes.", "pool", "dataset")
	zfsDatasetQuota       = gauge("zfs_dataset_quota_bytes", "Dataset quota in bytes; omitted when unlimited or inapplicable.", "pool", "dataset")
	zfsDatasetVolumeSize  = gauge("zfs_dataset_volume_size_bytes", "Logical size of a zvol in bytes.", "pool", "dataset")
	zfsDatasetCompression = gauge("zfs_dataset_compression_ratio", "Dataset compression ratio.", "pool", "dataset")
	zfsARCSize            = gauge("zfs_arc_size_bytes", "Host-wide ARC size in bytes.")
	zfsARCTarget          = gauge("zfs_arc_target_size_bytes", "Host-wide adaptive ARC target size in bytes.")
	zfsARCMin             = gauge("zfs_arc_min_size_bytes", "Host-wide minimum ARC target size in bytes.")
	zfsARCMax             = gauge("zfs_arc_max_size_bytes", "Host-wide maximum ARC target size in bytes.")
	zfsARCHits            = counter("zfs_arc_hits_total", "Host-wide ARC cache hits since ZFS initialization.")
	zfsARCMisses          = counter("zfs_arc_misses_total", "Host-wide ARC cache misses since ZFS initialization.")
)

var zfsDescriptors = []metric{
	zfsPoolInfo, zfsPoolHealth, zfsPoolSize, zfsPoolAllocated, zfsPoolFree, zfsPoolFragmentation, zfsPoolDedup,
	zfsPoolIOSuccess, zfsPoolReadIOPS, zfsPoolWriteIOPS, zfsPoolReadBytes, zfsPoolWriteBytes, zfsPoolReadLatency, zfsPoolWriteLatency,
	zfsDatasetInfo, zfsDatasetUsed, zfsDatasetAvailable, zfsDatasetReferenced, zfsDatasetLogicalUsed,
	zfsDatasetQuota, zfsDatasetVolumeSize, zfsDatasetCompression,
	zfsARCSize, zfsARCTarget, zfsARCMin, zfsARCMax, zfsARCHits, zfsARCMisses,
}

var zfsPoolStates = []string{"ONLINE", "DEGRADED", "FAULTED", "OFFLINE", "REMOVED", "UNAVAIL", "CORRUPT_DATA", "UNKNOWN"}

func collectZFS(ch chan<- prometheus.Metric, snapshot ZFSSnapshot, now time.Time) {
	if collectStatus(ch, "zfs_pools", snapshot.Pools, now) {
		for _, pool := range snapshot.Pools.Items {
			zfsPoolInfo.emit(ch, 1, pool.Name, pool.GUID)
			health := pool.Health
			if !slices.Contains(zfsPoolStates, health) {
				health = "UNKNOWN"
			}
			for _, state := range zfsPoolStates {
				zfsPoolHealth.emit(ch, boolValue(state == health), pool.Name, state)
			}
			zfsPoolSize.emitOptional(ch, pool.Size, pool.Name)
			zfsPoolAllocated.emitOptional(ch, pool.Allocated, pool.Name)
			zfsPoolFree.emitOptional(ch, pool.Free, pool.Name)
			zfsPoolFragmentation.emitOptional(ch, pool.FragmentationRatio, pool.Name)
			zfsPoolDedup.emitOptional(ch, pool.DedupRatio, pool.Name)
			ioFresh := now.Before(pool.IOExpiresAt)
			zfsPoolIOSuccess.emit(ch, boolValue(ioFresh), pool.Name)
			if ioFresh {
				zfsPoolReadIOPS.emit(ch, float64(pool.ReadIOPS), pool.Name)
				zfsPoolWriteIOPS.emit(ch, float64(pool.WriteIOPS), pool.Name)
				zfsPoolReadBytes.emit(ch, float64(pool.ReadBytesPerSecond), pool.Name)
				zfsPoolWriteBytes.emit(ch, float64(pool.WriteBytesPerSecond), pool.Name)
				zfsPoolReadLatency.emitOptional(ch, pool.ReadLatencySeconds, pool.Name)
				zfsPoolWriteLatency.emitOptional(ch, pool.WriteLatencySeconds, pool.Name)
			}
		}
	}
	if collectStatus(ch, "zfs_datasets", snapshot.Datasets, now) {
		for _, dataset := range snapshot.Datasets.Items {
			zfsDatasetInfo.emit(ch, 1, dataset.Pool, dataset.Name, dataset.GUID, dataset.Type)
			zfsDatasetUsed.emitOptional(ch, dataset.Used, dataset.Pool, dataset.Name)
			zfsDatasetAvailable.emitOptional(ch, dataset.Available, dataset.Pool, dataset.Name)
			zfsDatasetReferenced.emitOptional(ch, dataset.Referenced, dataset.Pool, dataset.Name)
			zfsDatasetLogicalUsed.emitOptional(ch, dataset.LogicalUsed, dataset.Pool, dataset.Name)
			zfsDatasetQuota.emitOptional(ch, dataset.Quota, dataset.Pool, dataset.Name)
			zfsDatasetVolumeSize.emitOptional(ch, dataset.VolumeSize, dataset.Pool, dataset.Name)
			zfsDatasetCompression.emitOptional(ch, dataset.CompressionRatio, dataset.Pool, dataset.Name)
		}
	}
	if collectStatus(ch, "zfs_arc", snapshot.ARC, now) {
		for _, arc := range snapshot.ARC.Items {
			zfsARCSize.emit(ch, float64(arc.Size))
			zfsARCTarget.emit(ch, float64(arc.TargetSize))
			zfsARCMin.emit(ch, float64(arc.MinSize))
			zfsARCMax.emit(ch, float64(arc.MaxSize))
			zfsARCHits.emit(ch, float64(arc.Hits))
			zfsARCMisses.emit(ch, float64(arc.Misses))
		}
	}
}

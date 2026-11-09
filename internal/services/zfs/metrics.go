// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/metrics"
)

// Space changes slowly, and staying under the 60s snapshot expiry is enough.
var datasetMetricsInterval = 30 * time.Second

func (s *Service) MetricsSnapshot() metrics.ZFSSnapshot {
	return metrics.ZFSSnapshot{
		Pools:    s.poolMetricsCache.Snapshot(),
		Datasets: s.datasetMetricsCache.Snapshot(),
		ARC:      s.arcMetricsCache.Snapshot(),
	}
}

func zfsBytes(value string) metrics.OptionalValue {
	number, err := strconv.ParseUint(value, 10, 64)
	return metrics.OptionalValue{Value: float64(number), Available: err == nil}
}

func zfsRatio(value, suffix string, divisor float64) metrics.OptionalValue {
	number, err := strconv.ParseFloat(strings.TrimSuffix(value, suffix), 64)
	return metrics.OptionalValue{Value: number / divisor, Available: err == nil && number >= 0 && !math.IsNaN(number) && !math.IsInf(number, 0)}
}

func poolMetricsSample(pool *gzfs.ZPool, io poolIOStat) metrics.ZFSPool {
	properties := pool.Properties
	sample := metrics.ZFSPool{
		Name: pool.Name, GUID: pool.PoolGUID, Health: string(pool.State),
		Size:               zfsBytes(properties["size"].Value),
		Allocated:          zfsBytes(properties["allocated"].Value),
		Free:               zfsBytes(properties["free"].Value),
		FragmentationRatio: zfsRatio(properties["fragmentation"].Value, "%", 100),
		DedupRatio:         zfsRatio(properties["dedupratio"].Value, "x", 1),
		ReadIOPS:           io.ReadIOPS, WriteIOPS: io.WriteIOPS,
		ReadBytesPerSecond: io.ReadBytesPerSecond, WriteBytesPerSecond: io.WriteBytesPerSecond,
		ReadLatencySeconds:  metrics.OptionalValue{Value: float64(io.ReadLatencyNanos) / float64(time.Second), Available: io.ReadLatencyAvailable},
		WriteLatencySeconds: metrics.OptionalValue{Value: float64(io.WriteLatencyNanos) / float64(time.Second), Available: io.WriteLatencyAvailable},
	}
	if !io.SampledAt.IsZero() {
		sample.IOExpiresAt = io.SampledAt.Add(poolIOStaleAfter)
	}
	return sample
}

// Space usage must be sampled afresh: the UI inventory cache is event-refreshed
// and may contain old capacity measurements even when the inventory is current.
func (s *Service) collectDatasetMetrics(ctx context.Context, scope managedPoolScope) {
	var samples []metrics.ZFSDataset
	failed := 0
	for _, pool := range scope.names {
		datasets, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetType("filesystem,volume"), true, pool)
		if err != nil {
			failed++
			logger.L.Debug().Err(err).Str("pool", pool).Msg("zfs_metrics: failed to list datasets")
			continue
		}
		// Podman layer datasets churn constantly; the images dataset's used totals them.
		layers := pool + "/" + containerModels.ImagesDataset + "/"
		for _, dataset := range datasets {
			if dataset == nil || (dataset.Name != pool && !strings.HasPrefix(dataset.Name, pool+"/")) || strings.HasPrefix(dataset.Name, layers) {
				continue
			}
			if dataset.Type != gzfs.DatasetTypeFilesystem && dataset.Type != gzfs.DatasetTypeVolume {
				continue
			}
			properties := dataset.Properties
			sample := metrics.ZFSDataset{
				Name: dataset.Name, Pool: pool, GUID: dataset.GUID, Type: strings.ToLower(string(dataset.Type)),
				Used:             zfsBytes(properties["used"].Value),
				Available:        zfsBytes(properties["available"].Value),
				Referenced:       zfsBytes(properties["referenced"].Value),
				LogicalUsed:      zfsBytes(properties["logicalused"].Value),
				CompressionRatio: zfsRatio(properties["compressratio"].Value, "x", 1),
			}
			if dataset.Type == gzfs.DatasetTypeVolume {
				sample.VolumeSize = zfsBytes(properties["volsize"].Value)
			} else {
				sample.Quota = zfsBytes(properties["quota"].Value)
				sample.Quota.Available = sample.Quota.Available && sample.Quota.Value > 0
			}
			samples = append(samples, sample)
		}
	}
	if failed > 0 && failed == len(scope.names) {
		s.datasetMetricsCache.Fail()
		return
	}
	s.datasetMetricsCache.Store(samples, failed == 0)
}

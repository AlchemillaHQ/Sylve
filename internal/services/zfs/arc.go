// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"fmt"
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/pkg/utils/sysctl"
)

const arcStatsPrefix = "kstat.zfs.misc.arcstats."

type arcStatField struct {
	name     string
	target   *uint64
	required bool
}

func collectARCStats(now time.Time) (infoModels.ZFSARCHistorical, error) {
	stat := infoModels.ZFSARCHistorical{CreatedAt: now}
	fields := []arcStatField{
		{name: "size", target: &stat.Size, required: true},
		{name: "c", target: &stat.TargetSize, required: true},
		{name: "c_min", target: &stat.MinSize, required: true},
		{name: "c_max", target: &stat.MaxSize, required: true},
		{name: "data_size", target: &stat.DataSize},
		{name: "metadata_size", target: &stat.MetadataSize},
		{name: "other_size", target: &stat.OtherSize},
		{name: "hdr_size", target: &stat.HeaderSize},
		{name: "compressed_size", target: &stat.CompressedSize},
		{name: "uncompressed_size", target: &stat.UncompressedSize},
		{name: "hits", target: &stat.Hits, required: true},
		{name: "misses", target: &stat.Misses, required: true},
		{name: "demand_data_hits", target: &stat.DemandDataHits},
		{name: "demand_data_misses", target: &stat.DemandDataMisses},
		{name: "demand_metadata_hits", target: &stat.DemandMetadataHits},
		{name: "demand_metadata_misses", target: &stat.DemandMetadataMisses},
		{name: "prefetch_data_hits", target: &stat.PrefetchDataHits},
		{name: "prefetch_data_misses", target: &stat.PrefetchDataMisses},
		{name: "prefetch_metadata_hits", target: &stat.PrefetchMetadataHits},
		{name: "prefetch_metadata_misses", target: &stat.PrefetchMetadataMisses},
		{name: "deleted", target: &stat.Deleted},
		{name: "evict_not_enough", target: &stat.EvictNotEnough},
		{name: "memory_throttle_count", target: &stat.MemoryThrottleCount},
		{name: "l2_ndev", target: &stat.L2DeviceCount},
		{name: "l2_size", target: &stat.L2Size},
		{name: "l2_asize", target: &stat.L2Allocated},
		{name: "l2_hits", target: &stat.L2Hits},
		{name: "l2_misses", target: &stat.L2Misses},
		{name: "l2_read_bytes", target: &stat.L2ReadBytes},
		{name: "l2_write_bytes", target: &stat.L2WriteBytes},
	}

	for _, field := range fields {
		value, err := sysctl.GetUint64(arcStatsPrefix + field.name)
		if err != nil {
			if field.required {
				return infoModels.ZFSARCHistorical{}, fmt.Errorf("read ARC statistic %s: %w", field.name, err)
			}
			continue
		}
		*field.target = value
	}

	return stat, nil
}

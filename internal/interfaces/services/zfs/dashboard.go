// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsServiceInterfaces

import "time"

type DashboardHistoryQuery struct {
	From      time.Time
	To        time.Time
	PoolGUID  string
	MaxPoints int
}

type DashboardDeltaQuery struct {
	PoolAfter uint
	ARCAfter  uint
	PoolGUID  string
}

type DashboardIOStats struct {
	SampledAt           int64   `json:"sampledAt"`
	IntervalSeconds     uint32  `json:"intervalSeconds"`
	Valid               bool    `json:"valid"`
	LatencyAvailable    bool    `json:"latencyAvailable"`
	ReadIOPS            uint64  `json:"readIOPS"`
	WriteIOPS           uint64  `json:"writeIOPS"`
	ReadBytesPerSecond  uint64  `json:"readBytesPerSecond"`
	WriteBytesPerSecond uint64  `json:"writeBytesPerSecond"`
	ReadLatencyNanos    *uint64 `json:"readLatencyNanos"`
	WriteLatencyNanos   *uint64 `json:"writeLatencyNanos"`
}

type DashboardPoolErrors struct {
	Read     uint64 `json:"read"`
	Write    uint64 `json:"write"`
	Checksum uint64 `json:"checksum"`
	Scan     uint64 `json:"scan"`
}

type DashboardPoolScan struct {
	Function        string   `json:"function"`
	State           string   `json:"state"`
	StartTime       string   `json:"startTime"`
	EndTime         string   `json:"endTime"`
	Examined        uint64   `json:"examined"`
	ToExamine       uint64   `json:"toExamine"`
	Issued          uint64   `json:"issued"`
	Processed       uint64   `json:"processed"`
	Errors          uint64   `json:"errors"`
	ProgressPercent *float64 `json:"progressPercent"`
}

type DashboardPoolTopology struct {
	DataVDEVs int `json:"dataVdevs"`
	Disks     int `json:"disks"`
	Logs      int `json:"logs"`
	Cache     int `json:"cache"`
	Spares    int `json:"spares"`
	Special   int `json:"special"`
	Dedup     int `json:"dedup"`
}

type DashboardPoolSnapshot struct {
	GUID            string                `json:"guid"`
	Name            string                `json:"name"`
	State           string                `json:"state"`
	Size            uint64                `json:"size"`
	Allocated       uint64                `json:"allocated"`
	Free            uint64                `json:"free"`
	Fragmentation   float64               `json:"fragmentation"`
	DedupRatio      float64               `json:"dedupRatio"`
	StatusAvailable bool                  `json:"statusAvailable"`
	Status          string                `json:"status"`
	Action          string                `json:"action"`
	Errors          DashboardPoolErrors   `json:"errors"`
	Scan            *DashboardPoolScan    `json:"scan"`
	Topology        DashboardPoolTopology `json:"topology"`
	IO              DashboardIOStats      `json:"io"`
}

type DashboardSnapshot struct {
	Pools       []DashboardPoolSnapshot `json:"pools"`
	ARC         *DashboardARCPoint      `json:"arc"`
	SampledAt   int64                   `json:"sampledAt"`
	GeneratedAt int64                   `json:"generatedAt"`
	Stale       bool                    `json:"stale"`
}

type DashboardPoolSeries struct {
	GUID   string          `json:"guid"`
	Name   string          `json:"name"`
	Points []PoolStatPoint `json:"points"`
}

type DashboardARCPoint struct {
	ID   uint  `json:"id"`
	Time int64 `json:"time"`

	Size             uint64 `json:"size"`
	TargetSize       uint64 `json:"targetSize"`
	MinSize          uint64 `json:"minSize"`
	MaxSize          uint64 `json:"maxSize"`
	DataSize         uint64 `json:"dataSize"`
	MetadataSize     uint64 `json:"metadataSize"`
	OtherSize        uint64 `json:"otherSize"`
	HeaderSize       uint64 `json:"headerSize"`
	CompressedSize   uint64 `json:"compressedSize"`
	UncompressedSize uint64 `json:"uncompressedSize"`

	HitRatio              *float64 `json:"hitRatio"`
	DemandHitRatio        *float64 `json:"demandHitRatio"`
	PrefetchHitRatio      *float64 `json:"prefetchHitRatio"`
	L2HitRatio            *float64 `json:"l2HitRatio"`
	EvictionsPerSecond    float64  `json:"evictionsPerSecond"`
	L2ReadBytesPerSecond  uint64   `json:"l2ReadBytesPerSecond"`
	L2WriteBytesPerSecond uint64   `json:"l2WriteBytesPerSecond"`

	MemoryThrottleEvents uint64 `json:"memoryThrottleEvents"`
	EvictNotEnoughEvents uint64 `json:"evictNotEnoughEvents"`
	L2DeviceCount        uint64 `json:"l2DeviceCount"`
	L2Size               uint64 `json:"l2Size"`
	L2Allocated          uint64 `json:"l2Allocated"`
}

type DashboardCursors struct {
	Pool uint `json:"pool"`
	ARC  uint `json:"arc"`
}

type DashboardHistory struct {
	Pools             []DashboardPoolSeries `json:"pools"`
	ARC               []DashboardARCPoint   `json:"arc"`
	Cursors           DashboardCursors      `json:"cursors"`
	ResolutionSeconds int64                 `json:"resolutionSeconds"`
	GeneratedAt       int64                 `json:"generatedAt"`
	ResetRequired     bool                  `json:"resetRequired"`
}

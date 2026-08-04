// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoModels

import "time"

type ZPoolHistorical struct {
	ID            uint    `json:"id" gorm:"primaryKey"`
	GUID          string  `json:"guid" gorm:"index:idx_zpool_history_guid_created_at,priority:1"`
	Name          string  `json:"name"`
	Health        string  `json:"health"`
	WorstHealth   string  `json:"worstHealth"`
	Allocated     uint64  `json:"allocated"`
	Size          uint64  `json:"size"`
	Free          uint64  `json:"free"`
	Fragmentation float64 `json:"fragmentation"`
	DedupRatio    float64 `json:"dedupRatio"`

	ReadIOPS               uint64 `json:"readIOPS"`
	WriteIOPS              uint64 `json:"writeIOPS"`
	ReadBytesPerSecond     uint64 `json:"readBytesPerSecond"`
	WriteBytesPerSecond    uint64 `json:"writeBytesPerSecond"`
	ReadLatencyNanos       uint64 `json:"readLatencyNanos"`
	WriteLatencyNanos      uint64 `json:"writeLatencyNanos"`
	MaxReadIOPS            uint64 `json:"maxReadIOPS"`
	MaxWriteIOPS           uint64 `json:"maxWriteIOPS"`
	MaxReadBytesPerSecond  uint64 `json:"maxReadBytesPerSecond"`
	MaxWriteBytesPerSecond uint64 `json:"maxWriteBytesPerSecond"`
	MaxReadLatencyNanos    uint64 `json:"maxReadLatencyNanos"`
	MaxWriteLatencyNanos   uint64 `json:"maxWriteLatencyNanos"`

	SampleCount     uint32 `json:"sampleCount" gorm:"not null;default:1"`
	IntervalSeconds uint32 `json:"intervalSeconds" gorm:"not null;default:10"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime;index;index:idx_zpool_history_guid_created_at,priority:2"`
}

func (z ZPoolHistorical) GetID() uint             { return z.ID }
func (z ZPoolHistorical) GetCreatedAt() time.Time { return z.CreatedAt }

type ZFSARCHistorical struct {
	ID uint `json:"id" gorm:"primaryKey"`

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

	Hits                   uint64 `json:"hits"`
	Misses                 uint64 `json:"misses"`
	DemandDataHits         uint64 `json:"demandDataHits"`
	DemandDataMisses       uint64 `json:"demandDataMisses"`
	DemandMetadataHits     uint64 `json:"demandMetadataHits"`
	DemandMetadataMisses   uint64 `json:"demandMetadataMisses"`
	PrefetchDataHits       uint64 `json:"prefetchDataHits"`
	PrefetchDataMisses     uint64 `json:"prefetchDataMisses"`
	PrefetchMetadataHits   uint64 `json:"prefetchMetadataHits"`
	PrefetchMetadataMisses uint64 `json:"prefetchMetadataMisses"`
	Deleted                uint64 `json:"deleted"`
	EvictNotEnough         uint64 `json:"evictNotEnough"`
	MemoryThrottleCount    uint64 `json:"memoryThrottleCount"`

	L2DeviceCount uint64 `json:"l2DeviceCount"`
	L2Size        uint64 `json:"l2Size"`
	L2Allocated   uint64 `json:"l2Allocated"`
	L2Hits        uint64 `json:"l2Hits"`
	L2Misses      uint64 `json:"l2Misses"`
	L2ReadBytes   uint64 `json:"l2ReadBytes"`
	L2WriteBytes  uint64 `json:"l2WriteBytes"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime;index"`
}

func (z ZFSARCHistorical) GetID() uint             { return z.ID }
func (z ZFSARCHistorical) GetCreatedAt() time.Time { return z.CreatedAt }

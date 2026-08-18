// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsServiceInterfaces

type RaidType string

const (
	RaidTypeStripe RaidType = "stripe"
	RaidTypeMirror RaidType = "mirror"
	RaidTypeRaidZ  RaidType = "raidz"
	RaidTypeRaidZ2 RaidType = "raidz2"
	RaidTypeRaidZ3 RaidType = "raidz3"
)

type VdevType string

const (
	VdevTypeData    VdevType = "data"
	VdevTypeLog     VdevType = "log"
	VdevTypeCache   VdevType = "cache"
	VdevTypeSpecial VdevType = "special"
	VdevTypeDedup   VdevType = "dedup"
)

type Vdev struct {
	Name        string   `json:"name"`
	Type        VdevType `json:"type"`
	RaidType    RaidType `json:"raidType"`
	VdevDevices []string `json:"devices"`
}

type CreateZPoolRequest struct {
	Name        string            `json:"name" binding:"required,alphanum,min=1,max=24"`
	Vdevs       []Vdev            `json:"vdevs"`
	Properties  map[string]string `json:"properties"`
	Mountpoint  string            `json:"mountpoint,omitempty"`
	CreateForce bool              `json:"createForce"`
	Spares      []string          `json:"spares"`
}

type ReplaceDevice struct {
	Old string `json:"old" binding:"required,min=1,max=24"`
	New string `json:"new" binding:"required,min=1,max=24"`
}

type PoolStatPoint struct {
	ID                         uint    `json:"id"`
	Time                       int64   `json:"time"`
	Health                     string  `json:"health"`
	WorstHealth                string  `json:"worstHealth"`
	Allocated                  uint64  `json:"allocated"`
	Free                       uint64  `json:"free"`
	Size                       uint64  `json:"size"`
	Fragmentation              float64 `json:"fragmentation"`
	DedupRatio                 float64 `json:"dedupRatio"`
	ReadIOPS                   uint64  `json:"readIOPS"`
	WriteIOPS                  uint64  `json:"writeIOPS"`
	ReadBytesPerSecond         uint64  `json:"readBytesPerSecond"`
	WriteBytesPerSecond        uint64  `json:"writeBytesPerSecond"`
	ReadLatencyNanos           uint64  `json:"readLatencyNanos"`
	WriteLatencyNanos          uint64  `json:"writeLatencyNanos"`
	MaxReadIOPS                uint64  `json:"maxReadIOPS"`
	MaxWriteIOPS               uint64  `json:"maxWriteIOPS"`
	MaxReadBytesPerSecond      uint64  `json:"maxReadBytesPerSecond"`
	MaxWriteBytesPerSecond     uint64  `json:"maxWriteBytesPerSecond"`
	MaxReadLatencyNanos        uint64  `json:"maxReadLatencyNanos"`
	MaxWriteLatencyNanos       uint64  `json:"maxWriteLatencyNanos"`
	SampleCount                uint32  `json:"sampleCount"`
	IntervalSeconds            uint32  `json:"intervalSeconds"`
}

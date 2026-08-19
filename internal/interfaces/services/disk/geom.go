// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package diskServiceInterfaces

import "encoding/xml"

type Mesh struct {
	XMLName xml.Name `xml:"mesh"`
	Classes []Class  `xml:"class"`
}

type Class struct {
	ID    string `xml:"id,attr"`
	Name  string `xml:"name"`
	Geoms []Geom `xml:"geom"`
}

type Geom struct {
	ID        string     `xml:"id,attr"`
	ClassRef  string     `xml:"ref,attr,omitempty"`
	Name      string     `xml:"name"`
	Rank      int        `xml:"rank"`
	Config    Config     `xml:"config,omitempty"`
	Providers []Provider `xml:"provider,omitempty"`
	Consumers []Consumer `xml:"consumer,omitempty"`
}

type Config struct {
	Scheme    string `xml:"scheme,omitempty"`
	Entries   int    `xml:"entries,omitempty"`
	First     int    `xml:"first,omitempty"`
	Last      int    `xml:"last,omitempty"`
	FwSectors int    `xml:"fwsectors,omitempty"`
	FwHeads   int    `xml:"fwheads,omitempty"`
	State     string `xml:"state,omitempty"`
	Modified  bool   `xml:"modified,omitempty"`

	Descr        string `xml:"descr,omitempty"`
	RotationRate string `xml:"rotationrate,omitempty"`
	Ident        string `xml:"ident,omitempty"`
	LunID        string `xml:"lunid,omitempty"`
	LunName      string `xml:"lunname,omitempty"`

	Index     int    `xml:"index,omitempty"`
	Length    int64  `xml:"length,omitempty"`
	SecLength int64  `xml:"seclength,omitempty"`
	Offset    int64  `xml:"offset,omitempty"`
	SecOffset int64  `xml:"secoffset,omitempty"`
	Start     int64  `xml:"start,omitempty"`
	End       int64  `xml:"end,omitempty"`
	Type      string `xml:"type,omitempty"`
	Label     string `xml:"label,omitempty"`
	RawType   string `xml:"rawtype,omitempty"`
	RawUUID   string `xml:"rawuuid,omitempty"`
	EFIMedia  string `xml:"efimedia,omitempty"`
}

type Provider struct {
	ID           string `xml:"id,attr"`
	GeomRef      string `xml:"geom,attr"`
	Mode         string `xml:"mode"`
	Name         string `xml:"name"`
	Alias        string `xml:"alias,omitempty"`
	MediaSize    int64  `xml:"mediasize"`
	SectorSize   int    `xml:"sectorsize"`
	StripeSize   int    `xml:"stripesize"`
	StripeOffset int64  `xml:"stripeoffset"`
	Config       Config `xml:"config,omitempty"`
}

type Consumer struct {
	ID          string `xml:"id,attr"`
	GeomRef     string `xml:"geom,attr"`
	ProviderRef string `xml:"provider,attr"`
	Mode        string `xml:"mode"`
	Config      Config `xml:"config,omitempty"`
}

type DiskInfo struct {
	Name         string
	Aliases      []string
	MediaSize    int64
	SectorSize   int
	Description  string
	RotationRate string
	Serial       string
	LunID        string
	Type         string
	IsISCSI      bool
	Partitions   []PartitionInfo
	IsBootDevice bool
}

type PartitionInfo struct {
	Name       string
	Aliases    []string
	Type       string
	Label      string
	Size       int64
	StartBlock int64
	EndBlock   int64
	Filesystem string
	GPT        bool
	UUID       string
	MountPoint string
}

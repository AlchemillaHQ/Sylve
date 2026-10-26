// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfsModels

import "time"

type NFSConfig struct {
	Protocol          string   `json:"protocol"`
	V4MaxVersion      string   `json:"v4MaxVersion"`
	NumericIDs        bool     `json:"numericIds"`
	UDP               bool     `json:"udp"`
	AllowNonroot      bool     `json:"allowNonroot"`
	BindAddresses     []string `json:"bindAddresses" gorm:"serializer:json;type:text"`
	MountdPort        uint16   `json:"mountdPort"`
	MinThreads        uint16   `json:"minThreads"`
	MaxThreads        uint16   `json:"maxThreads"`
	KerberosPrincipal string   `json:"kerberosPrincipal"`
}

type NFSSettings struct {
	ID        uint `json:"-" gorm:"primaryKey;autoIncrement:false;check:nfs_settings_singleton,id = 1"`
	NFSConfig `gorm:"embedded"`
}

type NFSShareConfig struct {
	Dataset      string   `json:"dataset" gorm:"not null;index"`
	Description  string   `json:"description"`
	Enabled      bool     `json:"enabled"`
	ReadOnly     bool     `json:"readOnly"`
	Everyone     bool     `json:"everyone"`
	Clients      []string `json:"clients" gorm:"serializer:json;type:text;not null"`
	MaprootUser  string   `json:"maprootUser"`
	MaprootGroup string   `json:"maprootGroup"`
	MapallUser   string   `json:"mapallUser"`
	MapallGroup  string   `json:"mapallGroup"`
	AllDirs      bool     `json:"allDirs"`
	Security     []string `json:"security" gorm:"serializer:json;type:text"`
}

type NFSShare struct {
	ID             uint `json:"id" gorm:"primaryKey;autoIncrement"`
	NFSShareConfig `gorm:"embedded"`
	DatasetName    string    `json:"datasetName"`
	Mountpoint     string    `json:"mountpoint"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type ProcessIdentity struct {
	PID   int    `json:"pid"`
	Birth string `json:"birth"`
}

type ExportedFilesystem struct {
	GUID       string `json:"guid"`
	Name       string `json:"name"`
	Mountpoint string `json:"mountpoint"`
}

type MappingIdentity struct {
	User       string
	Group      string
	Credential string
}

type NFSRuntime struct {
	ID            uint `gorm:"primaryKey;autoIncrement:false;check:nfs_runtime_singleton,id = 1"`
	Managed       bool
	ApplyStatus   string
	LastError     string
	Hash          string
	ConfigHash    string
	PolicyHash    string
	KerberosHash  string
	Mappings      []MappingIdentity    `gorm:"serializer:json;type:text"`
	BindAddresses []string             `gorm:"serializer:json;type:text"`
	NFSd          ProcessIdentity      `gorm:"serializer:json;type:text"`
	Mountd        ProcessIdentity      `gorm:"serializer:json;type:text"`
	Filesystems   []ExportedFilesystem `gorm:"serializer:json;type:text"`
	UpdatedAt     time.Time
}

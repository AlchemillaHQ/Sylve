// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilitiesServiceInterfaces

import (
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"time"
)

// ISOFile represents an ISO file found on the filesystem
type ISOFile struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Type    string    `json:"type"`   // "isos", "jail_templates", "vm_templates", "manual"
	Source  string    `json:"source"` // source URL or "manual"
}

type UtilitiesServiceInterface interface {
	DownloadFile(url string, optFilename string, downloadType string) error
	ListDownloads() ([]utilitiesModels.Downloads, error)
	GetDownload(filename string) (*utilitiesModels.Downloads, error)
	GetMagnetDownloadAndFile(filename, name string) (*utilitiesModels.Downloads, *utilitiesModels.DownloadedFile, error)
	GetFilePathById(filename string, id int) (string, error)
	SyncDownloadProgress() error
	DeleteDownload(filename string) error
	BulkDeleteDownload(filenames []string) error
	FindISOByName(name string) (*ISOFile, error)
	FindISOByPath(path string) (*ISOFile, error)
	GetDownloadProgress(gid string) (int, error)
	PauseDownload(gid string) error
	ResumeDownload(gid string) error
	CancelDownload(gid string) error
	GetFilePathByFilename(filename string) (string, error)
	Close() error

	StartWOLServer() error
}

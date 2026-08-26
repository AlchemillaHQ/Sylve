// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilitiesModels

import "time"

type UploadScope string

const (
	UploadScopeDownloader   UploadScope = "downloader"
	UploadScopeFileExplorer UploadScope = "file-explorer"
)

type UploadStatus string

const (
	UploadStatusStaged    UploadStatus = "staged"
	UploadStatusCompleted UploadStatus = "completed"
)

// Upload is the server-side resolution record for an opaque upload identity.
// Device and inode are retained solely so revert and cleanup cannot remove a
// different file that later appears at the same path.
type Upload struct {
	ID          string       `json:"uploadId" gorm:"primaryKey;size:36"`
	Scope       UploadScope  `json:"scope" gorm:"index;not null"`
	Path        string       `json:"-" gorm:"index;not null"`
	Name        string       `json:"name" gorm:"not null"`
	Size        int64        `json:"size" gorm:"not null"`
	UserID      uint         `json:"userId" gorm:"index;not null"`
	Node        string       `json:"node" gorm:"index;not null"`
	Status      UploadStatus `json:"status" gorm:"index;not null"`
	Device      int64        `json:"-" gorm:"not null"`
	Inode       int64        `json:"-" gorm:"not null"`
	CreatedAt   time.Time    `json:"createdAt" gorm:"autoCreateTime;index"`
	CompletedAt *time.Time   `json:"completedAt,omitempty"`
}

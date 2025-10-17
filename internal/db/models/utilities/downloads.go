// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilitiesModels

import "time"

type DownloadStatus string

const (
	DownloadStatusPending    DownloadStatus = "pending"
	DownloadStatusProcessing DownloadStatus = "processing"
	DownloadStatusDone       DownloadStatus = "done"
	DownloadStatusFailed     DownloadStatus = "failed"
)

type DownloadedFile struct {
	ID         int       `json:"id" gorm:"primaryKey"`
	DownloadID int       `json:"downloadId" gorm:"not null"`
	Download   Downloads `json:"download" gorm:"foreignKey:DownloadID;constraint:OnDelete:CASCADE"`
	Name       string    `json:"name" gorm:"not null"`
	Size       int64     `json:"size" gorm:"not null"`
}

type Downloads struct {
	ID            uint             `json:"id" gorm:"primaryKey"`
	UUID          string           `json:"uuid" gorm:"unique;not null"`
	Path          string           `json:"path" gorm:"unique;not null"`
	Name          string           `json:"name" gorm:"not null"`
	Type          string           `json:"type" gorm:"not null"`
	URL           string           `json:"url" gorm:"unique;not null"`
	Progress      int              `json:"progress" gorm:"not null"`
	Size          int64            `json:"size" gorm:"not null"`
	Files         []DownloadedFile `json:"files" gorm:"foreignKey:DownloadID;constraint:OnDelete:CASCADE"`
	UType         string           `json:"uType"` // fbsd-base etc.
	Error         string           `json:"error"`
	ExtractedPath string           `json:"extractedPath"`
	Status        DownloadStatus   `json:"status" gorm:"not null;default:'done'"`
	CreatedAt     time.Time        `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt     time.Time        `json:"updatedAt" gorm:"autoUpdateTime"`
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilitiesServiceInterfaces

import (
	"context"
	"time"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
)

type DownloadFileRequest struct {
	URL                    string                        `json:"url" binding:"required"`
	Filename               *string                       `json:"filename"`
	IgnoreTLS              *bool                         `json:"ignoreTLS"`
	AutomaticExtraction    *bool                         `json:"automaticExtraction"`
	AutomaticRawConversion *bool                         `json:"automaticRawConversion"`
	DownloadType           utilitiesModels.DownloadUType `json:"downloadType"`
}

type UTypeGroupedDownload struct {
	UUID  string                        `json:"uuid"`
	Label string                        `json:"label"`
	UType utilitiesModels.DownloadUType `json:"uType"`
}

type DownloadStartPayload struct {
	ID uint `json:"id"`
}

type DownloadStartResult struct {
	ID     uint                           `json:"id"`
	Status utilitiesModels.DownloadStatus `json:"status"`
}

type DownloadDeleteItem struct {
	ID   uint                         `json:"id"`
	UUID string                       `json:"uuid"`
	Name string                       `json:"name"`
	Type utilitiesModels.DownloadType `json:"type"`
}

type DownloadDeleteVMReference struct {
	StorageID uint   `json:"storageId"`
	VMID      uint   `json:"vmId"`
	VMRID     uint   `json:"vmRid"`
	VMName    string `json:"vmName"`
}

type DownloadDeleteFailure struct {
	ID            uint                         `json:"id"`
	UUID          string                       `json:"uuid"`
	Name          string                       `json:"name"`
	Type          utilitiesModels.DownloadType `json:"type"`
	Code          string                       `json:"code"`
	RetainedPaths []string                     `json:"retainedPaths"`
	VMReferences  []DownloadDeleteVMReference  `json:"vmReferences"`
}

type DownloadDeleteResult struct {
	Deleted []DownloadDeleteItem    `json:"deleted"`
	Failed  []DownloadDeleteFailure `json:"failed"`
}

type SignedDownloadURLResult struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type SignedDownloadTarget struct {
	ResourceID int                          `json:"resourceId"`
	UUID       string                       `json:"uuid"`
	Name       string                       `json:"name"`
	Path       string                       `json:"-"`
	Type       utilitiesModels.DownloadType `json:"type"`
}

type UpdateDownloadRequest struct {
	Name                   *string                        `json:"name"`
	UType                  *utilitiesModels.DownloadUType `json:"uType"`
	AutomaticExtraction    *bool                          `json:"automaticExtraction"`
	AutomaticRawConversion *bool                          `json:"automaticRawConversion"`
}

type DownloadPostProcPayload struct {
	ID uint `json:"id"`
}

type CompleteDownloaderUploadRequest struct {
	DownloadType           utilitiesModels.DownloadUType `json:"downloadType"`
	AutomaticExtraction    bool                          `json:"automaticExtraction"`
	AutomaticRawConversion bool                          `json:"automaticRawConversion"`
}

type DownloaderUploadCompletion struct {
	UploadID   string                       `json:"uploadId"`
	DownloadID uint                         `json:"downloadId"`
	Status     utilitiesModels.UploadStatus `json:"status"`
}

type UtilitiesServiceInterface interface {
	DownloadFile(req DownloadFileRequest) (uint, error)
	ListDownloads() ([]utilitiesModels.Downloads, error)
	SyncDownloadProgress() error
	DeleteDownload(id int) error

	RegisterJobs()
	StartUploadCleanupWorker(ctx context.Context)

	StartWOLServer() error
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilitiesServiceInterfaces

import utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"

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

type DownloadPostProcPayload struct {
	ID uint `json:"id"`
}

type UtilitiesServiceInterface interface {
	DownloadFile(req DownloadFileRequest) error
	ListDownloads() ([]utilitiesModels.Downloads, error)
	GetMagnetDownloadAndFile(uuid, name string) (*utilitiesModels.Downloads, *utilitiesModels.DownloadedFile, error)
	SyncDownloadProgress() error
	DeleteDownload(id int) error

	RegisterJobs()

	StartWOLServer() error
}

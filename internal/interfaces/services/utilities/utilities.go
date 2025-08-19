// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilitiesServiceInterfaces

import utilitiesModels "sylve/internal/db/models/utilities"

type UtilitiesServiceInterface interface {
	DownloadFile(url string, optFilename string) error
	ListDownloads() ([]utilitiesModels.Downloads, error)
	GetMagnetDownloadAndFile(uuid, name string) (*utilitiesModels.Downloads, *utilitiesModels.DownloadedFile, error)
	SyncDownloadProgress() error
	DeleteDownload(id int) error

	StartWOLServer() error
}

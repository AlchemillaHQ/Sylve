// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"

const (
	OperationDownloadList   = "downloads.list"
	OperationDownloadStart  = "downloads.start"
	OperationDownloadDelete = "downloads.delete"
)

type DownloadListPayload struct {
	JSON bool `json:"json"`
}

type DownloadStartPayload struct {
	Request utilitiesServiceInterfaces.DownloadFileRequest `json:"request"`
	JSON    bool                                           `json:"json"`
}

type DownloadDeletePayload struct {
	ID   uint `json:"id"`
	JSON bool `json:"json"`
}

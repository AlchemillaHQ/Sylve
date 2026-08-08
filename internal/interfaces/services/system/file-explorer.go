// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemServiceInterfaces

import (
	"io"
	"time"
)

type FileNode struct {
	ID   string    `json:"id"`
	Date time.Time `json:"date"`
	Type string    `json:"type"`
	Lazy bool      `json:"lazy,omitempty"`
	Size int64     `json:"size,omitempty"`
}

type FileTransferItem struct {
	Source      string `json:"source" binding:"required"`
	Destination string `json:"destination" binding:"required"`
}

type FileDownload struct {
	Reader  io.ReadSeekCloser
	Name    string
	ModTime time.Time
}

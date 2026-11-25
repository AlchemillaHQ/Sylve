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
	ID        string    `json:"id"`
	Date      time.Time `json:"date"`
	Type      string    `json:"type" enums:"file,folder"`
	Lazy      bool      `json:"lazy,omitempty"`
	Size      int64     `json:"size,omitempty"`
	Mode      string    `json:"mode,omitempty"`
	Owner     string    `json:"owner,omitempty"`
	Group     string    `json:"group,omitempty"`
	Link      string    `json:"link,omitempty"`
	Broken    bool      `json:"broken,omitempty"`
	Special   string    `json:"special,omitempty" enums:"fifo,socket,device"`
	Mount     string    `json:"mount,omitempty"`
	Protected string    `json:"protected,omitempty" enums:"root,system_folder,base_system,mount_point,contains_mount_point,jail_root,sylve_data,contains_sylve_data"`
}

type FileListing struct {
	Entries   []FileNode `json:"entries"`
	Total     int        `json:"total"`
	Matched   int        `json:"matched"`
	Truncated bool       `json:"truncated"`
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

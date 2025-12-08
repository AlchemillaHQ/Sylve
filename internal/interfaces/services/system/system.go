// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemServiceInterfaces

import (
	"context"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
)

type SystemServiceInterface interface {
	IsSupportedArch() bool
	CheckVirtualization() error
	CheckJails() error
	CheckDHCPServer() error
	CheckSambaServer() error

	GetUsablePools(ctx context.Context) ([]*gzfs.ZPool, error)
	Initialize(ctx context.Context, req InitializeRequest) []error

	Traverse(path string) ([]FileNode, error)
	AddFileOrFolder(path string, name string, isFolder bool) error
	DeleteFileOrFolder(path string) error
	DeleteFilesOrFolders(paths []string) error
	RenameFileOrFolder(oldPath string, newName string) error
	DownloadFile(id string) (string, error)
	CopyOrMoveFileOrFolder(source, destination string, move bool) error
	CopyOrMoveFilesOrFolders(pairs [][2]string, move bool) error

	SyncPPTDevices() error
	GetPPTDevices() ([]models.PassedThroughIDs, error)
	AddPPTDevice(domain string, id string) error
	RemovePPTDevice(id string) error
}

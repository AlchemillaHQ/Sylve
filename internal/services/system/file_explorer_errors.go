// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"errors"
	"fmt"
	"io/fs"
	"syscall"
)

const MaxFileExplorerBatchItems = 512

var (
	ErrFileExplorerInvalidPath       = errors.New("file_explorer_invalid_path")
	ErrFileExplorerInvalidName       = errors.New("file_explorer_invalid_name")
	ErrFileExplorerRootMutation      = errors.New("file_explorer_root_mutation_forbidden")
	ErrFileExplorerNotFound          = errors.New("file_explorer_entry_not_found")
	ErrFileExplorerNotDirectory      = errors.New("file_explorer_not_directory")
	ErrFileExplorerAlreadyExists     = errors.New("file_explorer_entry_already_exists")
	ErrFileExplorerPermissionDenied  = errors.New("file_explorer_permission_denied")
	ErrFileExplorerInvalidOperation  = errors.New("file_explorer_invalid_operation")
	ErrFileExplorerUnsupportedType   = errors.New("file_explorer_unsupported_file_type")
	ErrFileExplorerBatchConflict     = errors.New("file_explorer_batch_conflict")
	ErrFileExplorerBatchTooLarge     = errors.New("file_explorer_batch_too_large")
	ErrFileExplorerRestoreInProgress = errors.New("restore_in_progress")
	ErrFileExplorerOperationFailed   = errors.New("file_explorer_operation_failed")
)

func wrapFileExplorerError(kind error, resource string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", kind, resource)
	}
	return fmt.Errorf("%w: %s: %w", kind, resource, cause)
}

func wrapFileExplorerIOError(resource string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return wrapFileExplorerError(ErrFileExplorerNotFound, resource, err)
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.EROFS):
		return wrapFileExplorerError(ErrFileExplorerPermissionDenied, resource, err)
	case errors.Is(err, fs.ErrExist):
		return wrapFileExplorerError(ErrFileExplorerAlreadyExists, resource, err)
	case errors.Is(err, syscall.ENOTDIR):
		return wrapFileExplorerError(ErrFileExplorerNotDirectory, resource, err)
	default:
		return wrapFileExplorerError(ErrFileExplorerOperationFailed, resource, err)
	}
}

func FileExplorerErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrFileExplorerInvalidPath):
		return ErrFileExplorerInvalidPath.Error()
	case errors.Is(err, ErrFileExplorerInvalidName):
		return ErrFileExplorerInvalidName.Error()
	case errors.Is(err, ErrFileExplorerRootMutation):
		return ErrFileExplorerRootMutation.Error()
	case errors.Is(err, ErrFileExplorerNotFound):
		return ErrFileExplorerNotFound.Error()
	case errors.Is(err, ErrFileExplorerNotDirectory):
		return ErrFileExplorerNotDirectory.Error()
	case errors.Is(err, ErrFileExplorerAlreadyExists):
		return ErrFileExplorerAlreadyExists.Error()
	case errors.Is(err, ErrFileExplorerPermissionDenied):
		return ErrFileExplorerPermissionDenied.Error()
	case errors.Is(err, ErrFileExplorerInvalidOperation):
		return ErrFileExplorerInvalidOperation.Error()
	case errors.Is(err, ErrFileExplorerUnsupportedType):
		return ErrFileExplorerUnsupportedType.Error()
	case errors.Is(err, ErrFileExplorerBatchConflict):
		return ErrFileExplorerBatchConflict.Error()
	case errors.Is(err, ErrFileExplorerBatchTooLarge):
		return ErrFileExplorerBatchTooLarge.Error()
	case errors.Is(err, ErrFileExplorerRestoreInProgress):
		return ErrFileExplorerRestoreInProgress.Error()
	default:
		return ErrFileExplorerOperationFailed.Error()
	}
}

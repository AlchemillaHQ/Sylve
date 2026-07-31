// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/services/system"
	uploadCore "github.com/alchemillahq/sylve/internal/upload"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/semaphore"
)

const fileExplorerUploadField = "filepond"

type fileExplorerUploadPolicy struct {
	maxFileBytes    int64
	maxRequestBytes int64
}

type fileExplorerUploadErrorData struct {
	Code       string `json:"code"`
	Retryable  bool   `json:"retryable"`
	LimitBytes int64  `json:"limitBytes,omitempty"`
}

func configuredFileExplorerUploadPolicy() fileExplorerUploadPolicy {
	uploads := config.GetUploadsConfig()
	return normalizeFileExplorerUploadPolicy(fileExplorerUploadPolicy{
		maxFileBytes: uploads.MaxFileBytes,
	})
}

func normalizeFileExplorerUploadPolicy(policy fileExplorerUploadPolicy) fileExplorerUploadPolicy {
	if policy.maxFileBytes <= 0 {
		policy.maxFileBytes = config.DefaultUploadMaxFileBytes
	}
	if policy.maxRequestBytes <= 0 {
		policy.maxRequestBytes = uploadCore.RequestLimit(
			policy.maxFileBytes,
			config.DefaultUploadRequestOverheadBytes,
		)
	}
	return policy
}

func newFileExplorerUploadHandler(
	systemService *system.Service,
	policy fileExplorerUploadPolicy,
	admission *semaphore.Weighted,
) gin.HandlerFunc {
	policy = normalizeFileExplorerUploadPolicy(policy)
	return func(c *gin.Context) {
		destination, failure := validateFileExplorerUploadDestination(c, systemService)
		if failure != nil {
			writeFileExplorerUploadFailure(c, failure)
			return
		}

		reader, cleanup, receiveFailure := uploadCore.OpenMultipartRequest(
			c.Writer,
			c.Request,
			policy.maxRequestBytes,
		)
		if receiveFailure != nil {
			writeFileExplorerUploadFailure(c, receiveFailure)
			return
		}
		defer cleanup()

		if !admission.TryAcquire(1) {
			writeFileExplorerUploadFailure(c, uploadCore.CapacityFailure())
			return
		}
		defer admission.Release(1)

		receipt, failure := receiveFileExplorerMultipartUpload(
			c.Request.Context(),
			reader,
			destination,
			c.GetUint("UserID"),
			systemService,
			policy.maxFileBytes,
		)
		if failure != nil {
			writeFileExplorerUploadFailure(c, failure)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[UploadFileResponse]{
			Status:  "success",
			Message: "file_uploaded",
			Error:   "",
			Data:    receipt,
		})
	}
}

func validateFileExplorerUploadDestination(
	c *gin.Context,
	systemService *system.Service,
) (string, *uploadCore.Failure) {
	rawDestination := c.Query("path")
	if rawDestination == "" {
		return "", uploadCore.NewFailure(
			http.StatusBadRequest,
			"missing_path",
			errors.New("path query parameter is required"),
		)
	}

	destination := filepath.Clean(rawDestination)
	if !filepath.IsAbs(destination) {
		decodedDestination, err := url.PathUnescape(rawDestination)
		if err == nil {
			destination = filepath.Clean(decodedDestination)
		}
	}
	if !filepath.IsAbs(destination) {
		return "", uploadCore.NewFailure(
			http.StatusBadRequest,
			"invalid_destination",
			errors.New("destination path must be absolute"),
		)
	}

	info, err := os.Stat(destination)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return "", uploadCore.FilesystemFailure(err, "destination_stat_failed")
		}
		return "", uploadCore.NewFailure(
			http.StatusBadRequest,
			"invalid_destination",
			err,
		)
	}
	if !info.IsDir() {
		return "", uploadCore.NewFailure(
			http.StatusBadRequest,
			"invalid_destination",
			errors.New("destination path is not a directory"),
		)
	}

	resolvedDestination, err := filepath.EvalSymlinks(destination)
	if err != nil {
		return "", uploadCore.NewFailure(
			http.StatusBadRequest,
			"invalid_destination",
			err,
		)
	}
	if err := systemService.EnsureFileExplorerMutationAllowed(resolvedDestination); err != nil {
		return "", uploadCore.NewFailure(
			http.StatusConflict,
			"restore_in_progress",
			err,
		)
	}

	return resolvedDestination, nil
}

func receiveFileExplorerMultipartUpload(
	ctx context.Context,
	reader *multipart.Reader,
	destination string,
	userID uint,
	systemService *system.Service,
	maxFileBytes int64,
) (UploadFileResponse, *uploadCore.Failure) {
	staged, receiveFailure := uploadCore.ReceiveSingle(ctx, reader, uploadCore.ReceiveOptions{
		Field:        fileExplorerUploadField,
		MaxFileBytes: maxFileBytes,
		NormalizeName: func(name string) (string, error) {
			if err := utils.IsValidFilename(name); err != nil {
				return "", err
			}
			return name, nil
		},
		Open: func(name string) (*os.File, string, string, *uploadCore.Failure) {
			finalPath := filepath.Join(destination, name)
			if err := systemService.EnsureFileExplorerMutationAllowed(finalPath); err != nil {
				return nil, "", "", uploadCore.NewFailure(
					http.StatusConflict,
					"restore_in_progress",
					err,
				)
			}
			if _, err := os.Lstat(finalPath); err == nil {
				return nil, "", "", uploadCore.NewFailure(
					http.StatusConflict,
					"file_exists",
					errors.New("file already exists at destination"),
				)
			} else if !os.IsNotExist(err) {
				return nil, "", "", uploadCore.FilesystemFailure(err, "destination_stat_failed")
			}

			file, partialPath, err := uploadCore.CreateRandomPartial(destination)
			if err != nil {
				return nil, "", "", uploadCore.FilesystemFailure(err, "partial_create_failed")
			}
			return file, partialPath, finalPath, nil
		},
	})
	if receiveFailure != nil {
		return UploadFileResponse{}, receiveFailure
	}
	defer func() {
		if staged.PartialPath != "" {
			_ = os.Remove(staged.PartialPath)
		}
	}()

	if err := systemService.EnsureFileExplorerMutationAllowed(staged.FinalPath); err != nil {
		return UploadFileResponse{}, uploadCore.NewFailure(
			http.StatusConflict,
			"restore_in_progress",
			err,
		)
	}
	if err := uploadCore.PublishNoReplace(staged.PartialPath, staged.FinalPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return UploadFileResponse{}, uploadCore.NewFailure(
				http.StatusConflict,
				"file_exists",
				errors.New("file already exists at destination"),
			)
		}
		return UploadFileResponse{}, uploadCore.FilesystemFailure(err, "publish_failed")
	}
	staged.PartialPath = ""

	uploadID, err := systemService.RegisterFileExplorerUpload(staged.FinalPath, userID)
	if err != nil {
		cleanupErr := removePublishedUpload(staged.FinalPath, staged.FileInfo)
		if cleanupErr != nil {
			err = fmt.Errorf("%w; published file cleanup failed: %v", err, cleanupErr)
		}
		return UploadFileResponse{}, uploadCore.NewFailure(
			http.StatusInternalServerError,
			"upload_identity_failed",
			err,
		)
	}

	return UploadFileResponse{
		Path:     staged.FinalPath,
		UploadID: uploadID,
		Bytes:    staged.Bytes,
	}, nil
}

func removePublishedUpload(path string, expected os.FileInfo) error {
	identity, err := uploadCore.IdentityFromFileInfo(expected)
	if err != nil {
		return err
	}
	_, err = uploadCore.RemoveIfSame(path, identity)
	return err
}

func writeFileExplorerUploadFailure(c *gin.Context, failure *uploadCore.Failure) {
	if failure.RetryAfter != "" {
		c.Header("Retry-After", failure.RetryAfter)
	}
	c.JSON(failure.StatusCode, internal.APIResponse[fileExplorerUploadErrorData]{
		Status:  "error",
		Message: failure.Code,
		Error:   failure.Error(),
		Data: fileExplorerUploadErrorData{
			Code:       failure.Code,
			Retryable:  failure.Retryable,
			LimitBytes: failure.LimitBytes,
		},
	})
}

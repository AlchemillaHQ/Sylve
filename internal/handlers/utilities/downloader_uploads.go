// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilitiesHandlers

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	uploadCore "github.com/alchemillahq/sylve/internal/upload"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/semaphore"
)

const downloaderUploadField = "filepond"

type downloaderUploadPolicy struct {
	maxFileBytes    int64
	maxRequestBytes int64
}

type DownloaderUploadReceipt struct {
	UploadID string                       `json:"uploadId"`
	Name     string                       `json:"name"`
	Bytes    int64                        `json:"bytes"`
	Status   utilitiesModels.UploadStatus `json:"status"`
}

type DownloaderUploadAbortResponse struct {
	UploadID string `json:"uploadId"`
	Status   string `json:"status"`
}

type downloaderUploadErrorData struct {
	Code       string `json:"code"`
	Retryable  bool   `json:"retryable"`
	LimitBytes int64  `json:"limitBytes,omitempty"`
}

func configuredDownloaderUploadPolicy() downloaderUploadPolicy {
	uploads := config.GetUploadsConfig()
	return normalizeDownloaderUploadPolicy(downloaderUploadPolicy{
		maxFileBytes: uploads.MaxFileBytes,
	})
}

func normalizeDownloaderUploadPolicy(policy downloaderUploadPolicy) downloaderUploadPolicy {
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

func UploadDownloaderFile(
	utilitiesService *utilities.Service,
	admission *semaphore.Weighted,
) gin.HandlerFunc {
	return newDownloaderUploadHandler(
		utilitiesService,
		configuredDownloaderUploadPolicy(),
		config.GetDownloadsPath("uploads"),
		admission,
	)
}

func newDownloaderUploadHandler(
	utilitiesService *utilities.Service,
	policy downloaderUploadPolicy,
	stagingDirectory string,
	admission *semaphore.Weighted,
) gin.HandlerFunc {
	policy = normalizeDownloaderUploadPolicy(policy)

	return func(c *gin.Context) {
		if c.GetUint("UserID") == 0 {
			writeDownloaderUploadFailure(c, uploadCore.NewFailure(
				http.StatusUnauthorized,
				"upload_user_required",
				errors.New("an authenticated user is required"),
			))
			return
		}
		directory, err := canonicalDownloaderUploadDirectory(stagingDirectory)
		if err != nil {
			writeDownloaderUploadFailure(c, uploadCore.FilesystemFailure(err, "staging_unavailable"))
			return
		}

		reader, cleanup, receiveFailure := uploadCore.OpenMultipartRequest(
			c.Writer,
			c.Request,
			policy.maxRequestBytes,
		)
		if receiveFailure != nil {
			writeDownloaderUploadFailure(c, receiveFailure)
			return
		}
		defer cleanup()

		if !admission.TryAcquire(1) {
			writeDownloaderUploadFailure(c, uploadCore.CapacityFailure())
			return
		}
		defer admission.Release(1)

		uploadID := utils.GenerateRandomUUID()
		endActive := utilitiesService.BeginDownloaderUpload(uploadID)
		defer endActive()

		receipt, failure := receiveDownloaderMultipartUpload(
			c.Request.Context(),
			reader,
			directory,
			uploadID,
			c.GetUint("UserID"),
			utilitiesService,
			policy.maxFileBytes,
		)
		if failure != nil {
			writeDownloaderUploadFailure(c, failure)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[DownloaderUploadReceipt]{
			Status:  "success",
			Message: "downloader_upload_staged",
			Error:   "",
			Data:    receipt,
		})
	}
}

func canonicalDownloaderUploadDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("downloader upload staging path is not a directory")
	}
	return resolved, nil
}

func receiveDownloaderMultipartUpload(
	ctx context.Context,
	reader *multipart.Reader,
	directory string,
	uploadID string,
	userID uint,
	utilitiesService *utilities.Service,
	maxFileBytes int64,
) (DownloaderUploadReceipt, *uploadCore.Failure) {
	staged, receiveFailure := uploadCore.ReceiveSingle(ctx, reader, uploadCore.ReceiveOptions{
		Field:         downloaderUploadField,
		MaxFileBytes:  maxFileBytes,
		NormalizeName: sanitizeDownloaderUploadName,
		Open: func(name string) (*os.File, string, string, *uploadCore.Failure) {
			partialPath := filepath.Join(directory, utilities.DownloaderUploadPartialName(uploadID))
			finalPath := filepath.Join(directory, utilities.DownloaderUploadFinalName(uploadID))
			file, err := uploadCore.OpenExclusive(partialPath)
			if errors.Is(err, os.ErrExist) {
				failure := uploadCore.NewFailure(http.StatusConflict, "upload_id_collision", err)
				failure.Retryable = true
				return nil, "", "", failure
			}
			if err != nil {
				return nil, "", "", uploadCore.FilesystemFailure(err, "partial_create_failed")
			}
			return file, partialPath, finalPath, nil
		},
	})
	if receiveFailure != nil {
		return DownloaderUploadReceipt{}, receiveFailure
	}
	defer func() {
		if staged.PartialPath != "" {
			_ = os.Remove(staged.PartialPath)
		}
	}()

	if err := uploadCore.PublishNoReplace(staged.PartialPath, staged.FinalPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			failure := uploadCore.NewFailure(
				http.StatusConflict,
				"upload_id_collision",
				err,
			)
			failure.Retryable = true
			return DownloaderUploadReceipt{}, failure
		}
		return DownloaderUploadReceipt{}, uploadCore.FilesystemFailure(err, "publish_failed")
	}
	staged.PartialPath = ""

	_, err := utilitiesService.RegisterDownloaderUpload(
		ctx,
		uploadID,
		staged.FinalPath,
		staged.Name,
		staged.Bytes,
		userID,
		staged.FileInfo,
	)
	if err != nil {
		cleanupErr := removePublishedDownloaderUpload(staged.FinalPath, staged.FileInfo)
		if cleanupErr != nil {
			err = fmt.Errorf("%w; published file cleanup failed: %v", err, cleanupErr)
		}
		return DownloaderUploadReceipt{}, mapDownloaderUploadServiceFailure(err)
	}

	return DownloaderUploadReceipt{
		UploadID: uploadID,
		Name:     staged.Name,
		Bytes:    staged.Bytes,
		Status:   utilitiesModels.UploadStatusStaged,
	}, nil
}

func sanitizeDownloaderUploadName(raw string) (string, error) {
	name := filepath.Base(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"))
	if err := utils.IsValidFilename(name); err != nil {
		return "", err
	}
	return name, nil
}

func removePublishedDownloaderUpload(path string, expected os.FileInfo) error {
	identity, err := uploadCore.IdentityFromFileInfo(expected)
	if err != nil {
		return err
	}
	_, err = uploadCore.RemoveIfSame(path, identity)
	return err
}

func CompleteDownloaderUpload(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request utilitiesServiceInterfaces.CompleteDownloaderUploadRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeDownloaderUploadFailure(c, uploadCore.NewFailure(
				http.StatusBadRequest,
				"invalid_request",
				err,
			))
			return
		}

		result, err := utilitiesService.CompleteDownloaderUpload(
			c.Request.Context(),
			c.Param("id"),
			c.GetUint("UserID"),
			request,
		)
		if err != nil {
			writeDownloaderUploadFailure(c, mapDownloaderUploadServiceFailure(err))
			return
		}

		c.Set("AuditAsyncJobID", result.DownloadID)
		c.Set("AuditAsyncJobType", "file_download")
		c.JSON(http.StatusOK, internal.APIResponse[utilitiesServiceInterfaces.DownloaderUploadCompletion]{
			Status:  "success",
			Message: "downloader_upload_completed",
			Error:   "",
			Data:    result,
		})
	}
}

func AbortDownloaderUpload(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		completed, err := utilitiesService.AbortDownloaderUpload(
			c.Request.Context(),
			c.Param("id"),
			c.GetUint("UserID"),
		)
		if err != nil {
			writeDownloaderUploadFailure(c, mapDownloaderUploadServiceFailure(err))
			return
		}
		status := "aborted"
		if completed {
			status = string(utilitiesModels.UploadStatusCompleted)
		}
		c.JSON(http.StatusOK, internal.APIResponse[DownloaderUploadAbortResponse]{
			Status:  "success",
			Message: "downloader_upload_aborted",
			Error:   "",
			Data: DownloaderUploadAbortResponse{
				UploadID: c.Param("id"),
				Status:   status,
			},
		})
	}
}

func mapDownloaderUploadServiceFailure(err error) *uploadCore.Failure {
	switch {
	case errors.Is(err, utilities.ErrDownloaderUploadNotFound):
		return uploadCore.NewFailure(http.StatusNotFound, "upload_not_found", err)
	case errors.Is(err, utilities.ErrDownloaderUploadExpired):
		return uploadCore.NewFailure(http.StatusGone, "upload_expired", err)
	case errors.Is(err, utilities.ErrDownloaderUploadInvalid):
		return uploadCore.NewFailure(http.StatusBadRequest, "invalid_upload", err)
	case errors.Is(err, utilities.ErrDownloaderPostProcessOptions):
		return uploadCore.NewFailure(http.StatusUnprocessableEntity, "incompatible_post_processing_options", err)
	case errors.Is(err, utilities.ErrDownloaderUploadDestinationExists):
		return uploadCore.NewFailure(http.StatusConflict, "download_destination_exists", err)
	case errors.Is(err, utilities.ErrDownloaderUploadFileUnavailable):
		return uploadCore.NewFailure(http.StatusConflict, "upload_file_unavailable", err)
	case errors.Is(err, utilities.ErrDownloaderUploadActive):
		failure := uploadCore.NewFailure(http.StatusConflict, "upload_active", err)
		failure.Retryable = true
		failure.RetryAfter = "1"
		return failure
	case errors.Is(err, utilities.ErrDownloaderUploadQueue):
		failure := uploadCore.NewFailure(http.StatusServiceUnavailable, "download_queue_unavailable", err)
		failure.Retryable = true
		failure.RetryAfter = "1"
		return failure
	case errors.Is(err, utilities.ErrDownloaderUploadPersistence):
		failure := uploadCore.NewFailure(http.StatusServiceUnavailable, "upload_persistence_unavailable", err)
		failure.Retryable = true
		failure.RetryAfter = "1"
		return failure
	default:
		return uploadCore.NewFailure(http.StatusInternalServerError, "downloader_upload_failed", err)
	}
}

func writeDownloaderUploadFailure(c *gin.Context, failure *uploadCore.Failure) {
	if failure.RetryAfter != "" {
		c.Header("Retry-After", failure.RetryAfter)
	}
	c.JSON(failure.StatusCode, internal.APIResponse[downloaderUploadErrorData]{
		Status:  "error",
		Message: failure.Code,
		Error:   failure.Error(),
		Data: downloaderUploadErrorData{
			Code:       failure.Code,
			Retryable:  failure.Retryable,
			LimitBytes: failure.LimitBytes,
		},
	})
}

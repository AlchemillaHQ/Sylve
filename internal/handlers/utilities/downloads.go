// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilitiesHandlers

import (
	"errors"
	"mime"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
)

type BulkDeleteDownloadRequest struct {
	IDs []int `json:"ids" binding:"required,min=1,dive,gt=0"`
}

func signedDownloadMintError(err error) (int, string) {
	switch {
	case errors.Is(err, utilities.ErrSignedDownloadInvalid):
		return http.StatusBadRequest, "invalid_signed_download_request"
	case errors.Is(err, utilities.ErrSignedDownloadNotFound):
		return http.StatusNotFound, "signed_download_not_found"
	case errors.Is(err, utilities.ErrSignedDownloadNotReady):
		return http.StatusConflict, "signed_download_not_ready"
	default:
		return http.StatusInternalServerError, "failed_to_create_signed_download_url"
	}
}

func writePublicDownloadError(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   message,
		Data:    nil,
	})
}

type SignedURLRequest struct {
	Name       string `json:"name" binding:"required"`
	ParentUUID string `json:"parentUUID" binding:"required"`
}

type DownloadPathsResponse struct {
	HTTP string `json:"http"`
	Path string `json:"path"`
}

func utilitiesJSONBindError(err error) (int, string) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return http.StatusRequestEntityTooLarge, "request_too_large"
	}
	return http.StatusBadRequest, "invalid_request"
}

func writeUtilitiesJSONBindError(c *gin.Context, err error) {
	status, message := utilitiesJSONBindError(err)
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: message,
		Error:   message,
		Data:    nil,
	})
}

func downloadMutationError(err error, fallback string) (int, string) {
	switch {
	case errors.Is(err, utilities.ErrDownloadInvalid):
		return http.StatusBadRequest, "invalid_download_request"
	case errors.Is(err, utilities.ErrDownloadNotFound):
		return http.StatusNotFound, "download_not_found"
	case errors.Is(err, utilities.ErrDownloadConflict):
		return http.StatusConflict, "download_conflict"
	case errors.Is(err, utilities.ErrDownloadActive):
		return http.StatusConflict, "download_active"
	case errors.Is(err, utilities.ErrDownloadInUse):
		return http.StatusConflict, "download_in_use"
	case errors.Is(err, utilities.ErrDownloadCleanup):
		return http.StatusInternalServerError, "download_cleanup_failed"
	case errors.Is(err, utilities.ErrDownloaderPostProcessOptions):
		return http.StatusUnprocessableEntity, "incompatible_post_processing_options"
	case errors.Is(err, utilities.ErrDownloadUnprocessable):
		return http.StatusUnprocessableEntity, "download_request_unprocessable"
	case errors.Is(err, utilities.ErrDownloadQueueUnavailable):
		return http.StatusServiceUnavailable, "download_queue_unavailable"
	default:
		return http.StatusInternalServerError, fallback
	}
}

// @Summary Get Download Paths
// @Description Get configured filesystem paths used by downloader for HTTP and Path downloads
// @Tags Utilities
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[DownloadPathsResponse] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Router /utilities/downloads/paths [get]
func GetDownloadPaths() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, internal.APIResponse[DownloadPathsResponse]{
			Status:  "success",
			Message: "download_paths_retrieved",
			Error:   "",
			Data: DownloadPathsResponse{
				HTTP: config.GetDownloadsPath("http"),
				Path: config.GetDownloadsPath("path"),
			},
		})
	}
}

// @Summary List Downloads
// @Description List downloader records and their current transfer or processing state
// @Tags Utilities
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]utilitiesModels.Downloads] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/downloads [get]
func ListDownloads(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		downloads, err := utilitiesService.ListDownloads()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_downloads",
				Error:   "internal_error",
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]utilitiesModels.Downloads]{
			Status:  "success",
			Message: "downloads_listed",
			Error:   "",
			Data:    downloads,
		})
	}
}

// @Summary List Completed Download Choices
// @Description List compact completed-download choices for consumers such as VM, Jail, and ZFS workflows
// @Tags Utilities
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]utilitiesServiceInterfaces.UTypeGroupedDownload] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/downloads/utype [get]
func ListDownloadsByUType(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupedDownloads, err := utilitiesService.ListDownloadsByUType()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_downloads_by_utype",
				Error:   "internal_error",
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]utilitiesServiceInterfaces.UTypeGroupedDownload]{
			Status:  "success",
			Message: "downloads_grouped_by_utype_listed",
			Error:   "",
			Data:    groupedDownloads,
		})
	}
}

// @Summary Start Download
// @Description Persist and queue a download from a magnet URI, HTTP(S) URL, or readable regular absolute path
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body utilitiesServiceInterfaces.DownloadFileRequest true "Download request"
// @Success 202 {object} internal.APIResponse[utilitiesServiceInterfaces.DownloadStartResult] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Download or destination already exists"
// @Failure 413 {object} internal.APIResponse[any] "Request body too large"
// @Failure 422 {object} internal.APIResponse[any] "Unsupported source or processing options"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[utilitiesServiceInterfaces.DownloadStartResult] "Queue unavailable"
// @Router /utilities/downloads [post]
func DownloadFile(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request utilitiesServiceInterfaces.DownloadFileRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeUtilitiesJSONBindError(c, err)
			return
		}

		downloadID, err := utilitiesService.DownloadFile(request)
		result := utilitiesServiceInterfaces.DownloadStartResult{
			ID:     downloadID,
			Status: utilitiesModels.DownloadStatusPending,
		}
		if err != nil {
			status, message := downloadMutationError(err, "failed_to_start_download")
			var data any
			if downloadID > 0 {
				data = result
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: message,
				Error:   message,
				Data:    data,
			})
			return
		}

		c.Set("AuditAsyncJobID", downloadID)
		c.Set("AuditAsyncJobType", "file_download")

		c.JSON(http.StatusAccepted, internal.APIResponse[utilitiesServiceInterfaces.DownloadStartResult]{
			Status:  "success",
			Message: "file_download_accepted",
			Error:   "",
			Data:    result,
		})
	}
}

// @Summary Delete Download
// @Description Delete one download after reference, activity, and managed-path preflight; unexpected cleanup failures return a typed retained-path result
// @Tags Utilities
// @Produce json
// @Security BearerAuth
// @Param id path int true "Download ID" minimum(1)
// @Success 200 {object} internal.APIResponse[utilitiesServiceInterfaces.DownloadDeleteResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[utilitiesServiceInterfaces.DownloadDeleteResult] "Download not found"
// @Failure 409 {object} internal.APIResponse[utilitiesServiceInterfaces.DownloadDeleteResult] "Download active, referenced, or unsafe to clean"
// @Failure 500 {object} internal.APIResponse[utilitiesServiceInterfaces.DownloadDeleteResult] "Cleanup or persistence failure"
// @Router /utilities/downloads/{id} [delete]
func DeleteDownload(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := utils.GetIdFromParam(c)

		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   "invalid_request",
				Data:    nil,
			})
			return
		}

		result, err := utilitiesService.DeleteDownloads([]int{id})
		if err != nil {
			status, message := downloadMutationError(err, "failed_to_delete_download")
			c.JSON(status, internal.APIResponse[utilitiesServiceInterfaces.DownloadDeleteResult]{
				Status:  "error",
				Message: message,
				Error:   message,
				Data:    result,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[utilitiesServiceInterfaces.DownloadDeleteResult]{
			Status:  "success",
			Message: "download_deleted",
			Error:   "",
			Data:    result,
		})
	}
}

// @Summary Bulk Delete Downloads
// @Description Delete unique downloads after strict all-member preflight; missing, referenced, active, or unsafe members prevent all deletion, while unexpected cleanup failures return typed partial results
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body BulkDeleteDownloadRequest true "Bulk Delete Download Request"
// @Success 200 {object} internal.APIResponse[utilitiesServiceInterfaces.DownloadDeleteResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[utilitiesServiceInterfaces.DownloadDeleteResult] "One or more downloads not found; nothing deleted"
// @Failure 409 {object} internal.APIResponse[utilitiesServiceInterfaces.DownloadDeleteResult] "One or more downloads active, referenced, or unsafe; nothing deleted"
// @Failure 413 {object} internal.APIResponse[any] "Request body too large"
// @Failure 500 {object} internal.APIResponse[utilitiesServiceInterfaces.DownloadDeleteResult] "Cleanup or persistence failure, possibly partial"
// @Router /utilities/downloads/bulk-delete [post]
func BulkDeleteDownload(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request BulkDeleteDownloadRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeUtilitiesJSONBindError(c, err)
			return
		}

		result, err := utilitiesService.DeleteDownloads(request.IDs)
		if err != nil {
			status, message := downloadMutationError(err, "failed_to_bulk_delete_downloads")
			c.JSON(status, internal.APIResponse[utilitiesServiceInterfaces.DownloadDeleteResult]{
				Status:  "error",
				Message: message,
				Error:   message,
				Data:    result,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[utilitiesServiceInterfaces.DownloadDeleteResult]{
			Status:  "success",
			Message: "downloads_bulk_deleted",
			Error:   "",
			Data:    result,
		})
	}
}

// @Summary Get Signed Download URL
// @Description Create a two-hour public capability URL for one completed download or torrent member
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body SignedURLRequest true "Signed URL Request"
// @Success 200 {object} internal.APIResponse[utilitiesServiceInterfaces.SignedDownloadURLResult] "Capability created"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access required"
// @Failure 404 {object} internal.APIResponse[any] "Download or torrent member not found"
// @Failure 409 {object} internal.APIResponse[any] "Download is not complete"
// @Failure 413 {object} internal.APIResponse[any] "Request body too large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/downloads/signed-url [post]
func GetSignedDownloadURL(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request SignedURLRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeUtilitiesJSONBindError(c, err)
			return
		}

		result, err := utilitiesService.CreateSignedDownloadURL(request.ParentUUID, request.Name)
		if err != nil {
			status, message := signedDownloadMintError(err)
			if status == http.StatusInternalServerError {
				logger.L.Error().Err(err).Msg("Failed to create signed download URL")
			}
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: message,
				Error:   message,
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[utilitiesServiceInterfaces.SignedDownloadURLResult]{
			Status:  "success",
			Message: "signed_url_generated",
			Error:   "",
			Data:    result,
		})
	}
}

// @Summary Update Download
// @Description Partially update completed or failed download display metadata and processing options; active downloads cannot be changed
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Download ID" minimum(1)
// @Param request body utilitiesServiceInterfaces.UpdateDownloadRequest true "Update Download Request"
// @Success 200 {object} internal.APIResponse[utilitiesModels.Downloads] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Download not found"
// @Failure 409 {object} internal.APIResponse[any] "Download is active"
// @Failure 413 {object} internal.APIResponse[any] "Request body too large"
// @Failure 422 {object} internal.APIResponse[any] "Unsupported metadata or processing options"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Queue unavailable"
// @Router /utilities/downloads/{id} [patch]
func UpdateDownload(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := utils.GetIdFromParam(c)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   "invalid_request",
				Data:    nil,
			})
			return
		}

		var request utilitiesServiceInterfaces.UpdateDownloadRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeUtilitiesJSONBindError(c, err)
			return
		}

		updated, err := utilitiesService.UpdateDownload(uint(id), request)
		if err != nil {
			status, message := downloadMutationError(err, "failed_to_update_download")
			c.JSON(status, internal.APIResponse[any]{
				Status:  "error",
				Message: message,
				Error:   message,
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[utilitiesModels.Downloads]{
			Status:  "success",
			Message: "download_updated",
			Error:   "",
			Data:    *updated,
		})
	}
}

// @Summary Download File
// @Description Download exactly one file using an unexpired node-bound capability URL; no Bearer token is required
// @Tags Utilities
// @Produce octet-stream
// @Param uuid path string true "Download UUID"
// @Param expires query int true "Expiration time in Unix timestamp"
// @Param sig query string true "Signature"
// @Param id query int true "Download or torrent member ID" minimum(1)
// @Param node query string true "Serving cluster node hostname"
// @Success 200 {file} file "File content"
// @Success 206 {file} file "Partial file content"
// @Failure 400 {object} internal.APIResponse[any] "Malformed capability"
// @Failure 403 {object} internal.APIResponse[any] "Invalid, expired, or tampered capability"
// @Failure 404 {object} internal.APIResponse[any] "Capability target or selected node not found"
// @Failure 416 {string} string "Range Not Satisfiable"
// @Failure 500 {object} internal.APIResponse[any] "Unexpected signing, database, or filesystem failure"
// @Failure 502 {object} internal.APIResponse[any] "Selected node forwarding failed"
// @Failure 503 {object} internal.APIResponse[any] "Selected node is offline or unavailable"
// @Router /utilities/downloads/{uuid} [get]
func DownloadFileFromSignedURL(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")

		uuid := c.Param("uuid")
		expiresStr := c.Query("expires")
		sig := c.Query("sig")
		idStr := c.Query("id")
		node := c.Query("node")

		if uuid == "" || expiresStr == "" || sig == "" || idStr == "" || node == "" {
			writePublicDownloadError(c, http.StatusBadRequest, "missing_required_params")
			return
		}

		expires, err := strconv.ParseInt(expiresStr, 10, 64)
		if err != nil || expires <= 0 {
			writePublicDownloadError(c, http.StatusBadRequest, "invalid_expiration")
			return
		}

		id, err := strconv.Atoi(idStr)
		if err != nil || id <= 0 {
			writePublicDownloadError(c, http.StatusBadRequest, "invalid_file_id")
			return
		}

		now := time.Now()
		if expires <= now.Unix() || expires > now.Add(utilities.SignedDownloadURLValidity+time.Minute).Unix() {
			writePublicDownloadError(c, http.StatusForbidden, "invalid_or_expired_signature")
			return
		}

		validSig, sigErr := utilitiesService.ValidateSignedDownloadSignature(node, uuid, id, expires, sig)
		if sigErr != nil {
			if errors.Is(sigErr, utilities.ErrSignedDownloadInvalid) {
				writePublicDownloadError(c, http.StatusBadRequest, "invalid_capability")
				return
			}
			logger.L.Error().Err(sigErr).Msg("Failed to validate signed download capability")
			writePublicDownloadError(c, http.StatusInternalServerError, "failed_to_validate_signature")
			return
		}
		if !validSig {
			writePublicDownloadError(c, http.StatusForbidden, "invalid_or_expired_signature")
			return
		}

		target, err := utilitiesService.ResolveSignedDownloadTargetByID(uuid, id)
		if err != nil {
			switch {
			case errors.Is(err, utilities.ErrSignedDownloadNotFound),
				errors.Is(err, utilities.ErrSignedDownloadNotReady):
				writePublicDownloadError(c, http.StatusNotFound, "file_not_found")
			case errors.Is(err, utilities.ErrSignedDownloadInvalid):
				writePublicDownloadError(c, http.StatusBadRequest, "invalid_capability")
			default:
				logger.L.Error().Err(err).Msg("Failed to resolve signed download target")
				writePublicDownloadError(c, http.StatusInternalServerError, "failed_to_resolve_download")
			}
			return
		}

		before, err := os.Lstat(target.Path)
		if errors.Is(err, os.ErrNotExist) {
			writePublicDownloadError(c, http.StatusNotFound, "file_not_found")
			return
		}
		if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
			logger.L.Error().Err(err).Str("path", target.Path).Msg("Signed download target changed before open")
			writePublicDownloadError(c, http.StatusInternalServerError, "failed_to_open_download")
			return
		}

		file, err := os.Open(target.Path)
		if errors.Is(err, os.ErrNotExist) {
			writePublicDownloadError(c, http.StatusNotFound, "file_not_found")
			return
		}
		if err != nil {
			logger.L.Error().Err(err).Str("path", target.Path).Msg("Failed to open signed download target")
			writePublicDownloadError(c, http.StatusInternalServerError, "failed_to_open_download")
			return
		}
		defer file.Close()

		after, err := file.Stat()
		if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
			logger.L.Error().Err(err).Str("path", target.Path).Msg("Signed download target changed while opening")
			writePublicDownloadError(c, http.StatusInternalServerError, "failed_to_open_download")
			return
		}

		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": target.Name})
		if disposition == "" {
			writePublicDownloadError(c, http.StatusInternalServerError, "failed_to_prepare_download")
			return
		}
		c.Header("Content-Disposition", disposition)
		http.ServeContent(c.Writer, c.Request, target.Name, after.ModTime(), file)
	}
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilitiesHandlers

import (
	"fmt"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/alchemillahq/sylve/internal"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/pkg/crypto"

	"github.com/gin-gonic/gin"
)

type DownloadFileRequest struct {
	URL          string  `json:"url" binding:"required"`
	Filename     *string `json:"filename"`
	DownloadType string  `json:"downloadType"`
}

type BulkDeleteDownloadRequest struct {
	Filenames []string `json:"filenames" binding:"required"`
}

type SignedURLRequest struct {
	Name     string `json:"name" binding:"required"`
	Filename string `json:"filename" binding:"required"`
}

// @Summary List Downloads
// @Description List all downloads
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]utilitiesModels.Downloads] "Success"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/downloads [get]
func ListDownloads(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		downloads, err := utilitiesService.ListDownloads()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_downloads",
				Error:   err.Error(),
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

// @Summary Download File
// @Description Download a file from a Magnet or HTTP(s) URL
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Request body DownloadFileRequest true "Download File Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/downloads [post]
func DownloadFile(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request DownloadFileRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		var fileName string
		if request.Filename != nil && *request.Filename != "" {
			fileName = *request.Filename
		} else {
			fileName = ""
		}

		// Default to ISOs if no download type specified
		downloadType := request.DownloadType
		if downloadType == "" {
			downloadType = "isos"
		}

		if err := utilitiesService.DownloadFile(request.URL, fileName, downloadType); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_download_file",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "file_download_started",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete Download
// @Description Delete a download by its filename
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param filename path string true "Download Filename"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/downloads/{filename} [delete]
func DeleteDownload(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		filename := c.Param("filename")
		if filename == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "filename_required",
				Error:   "filename parameter is required",
				Data:    nil,
			})
			return
		}

		if err := utilitiesService.DeleteDownload(filename); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_delete_download",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "download_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Bulk Delete Downloads
// @Description Bulk delete downloads by their filenames
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Request body BulkDeleteDownloadRequest true "Bulk Delete Download Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/downloads/bulk-delete [post]
func BulkDeleteDownload(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request BulkDeleteDownloadRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := utilitiesService.BulkDeleteDownload(request.Filenames); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_bulk_delete_downloads",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "downloads_bulk_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Get Signed Download URL
// @Description Get a signed URL for downloading a file
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Request body SignedURLRequest true "Signed URL Request"
// @Success 200 {object} internal.APIResponse[string] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/downloads/signed-url [post]
func GetSignedDownloadURL(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request SignedURLRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		download, err := utilitiesService.GetDownload(request.Filename)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_download",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		expires := time.Now().Add(2 * time.Hour).Unix()

		// For backward compatibility, we still support the old signature format
		// but now use filename instead of UUID
		input := fmt.Sprintf("%s:%s", download.Name, request.Name)
		sig := crypto.GenerateSignature(input, expires, []byte("download_secret"))
		signedURL := fmt.Sprintf("/api/utilities/downloads/%s?expires=%d&sig=%s", download.Name, expires, sig)

		c.JSON(http.StatusOK, internal.APIResponse[string]{
			Status:  "success",
			Message: "signed_url_generated",
			Error:   "",
			Data:    signedURL,
		})
	}
}

// @Summary Download File
// @Description Download a file from a signed URL
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param filename path string true "Download Filename"
// @Param expires query int true "Expiration time in Unix timestamp"
// @Param sig query string true "Signature"
// @Success 200 {file} file "File Download"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/downloads/{filename} [get]
func DownloadFileFromSignedURL(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		filename := c.Param("filename")
		expiresStr := c.Query("expires")
		sig := c.Query("sig")

		if filename == "" || expiresStr == "" || sig == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_required_params",
			})
			return
		}

		expires, err := strconv.ParseInt(expiresStr, 10, 64)
		if err != nil || time.Now().Unix() > expires {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_or_expired_signature",
			})
			return
		}

		// Verify signature - we use a simplified approach now
		// Since we're using filename-based identification, we can simplify the signature
		input := fmt.Sprintf("%s", filename)
		expectedSig := crypto.GenerateSignature(input, expires, []byte("download_secret"))
		if sig != expectedSig {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "signature_mismatch",
			})
			return
		}

		// Get file path by filename (id is not used anymore, but we pass 0 for compatibility)
		filePath, err := utilitiesService.GetFilePathById(filename, 0)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "file_not_found",
				Error:   err.Error(),
			})
			return
		}

		c.FileAttachment(filePath, path.Base(filePath))
	}
}

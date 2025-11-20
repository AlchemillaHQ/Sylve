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
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
)

type DownloadFileRequest struct {
	URL                 string                        `json:"url" binding:"required"`
	Filename            *string                       `json:"filename"`
	IgnoreTLS           *bool                         `json:"ignoreTLS"`
	AutomaticExtraction *bool                         `json:"automaticExtraction"`
	DownloadType        utilitiesModels.DownloadUType `json:"downloadType"`
}

type BulkDeleteDownloadRequest struct {
	IDs []int `json:"ids" binding:"required"`
}

type SignedURLRequest struct {
	Name       string `json:"name" binding:"required"`
	ParentUUID string `json:"parentUUID" binding:"required"`
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

		var insecureOkay bool
		if request.IgnoreTLS != nil && *request.IgnoreTLS {
			insecureOkay = true
		} else {
			insecureOkay = false
		}

		var automaticExtraction bool
		if request.AutomaticExtraction != nil && *request.AutomaticExtraction {
			automaticExtraction = true
		} else {
			automaticExtraction = false
		}

		if err := utilitiesService.DownloadFile(request.URL, fileName, insecureOkay, automaticExtraction, request.DownloadType); err != nil {
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
// @Description Delete a download by its ID
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Download ID"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/downloads/{id} [delete]
func DeleteDownload(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := utils.GetIdFromParam(c)

		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := utilitiesService.DeleteDownload(id); err != nil {
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
// @Description Bulk delete downloads by their IDs
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

		if err := utilitiesService.BulkDeleteDownload(request.IDs); err != nil {
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
// @Router /utilities/downloads/signed-url [get]
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
		}

		download, err := utilitiesService.GetDownload(request.ParentUUID)
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

		if download.Type == "torrent" {
			download, file, err := utilitiesService.GetMagnetDownloadAndFile(request.ParentUUID, request.Name)
			if err != nil {
				c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
					Status:  "error",
					Message: "failed_to_get_download",
					Error:   err.Error(),
					Data:    nil,
				})
			}

			input := fmt.Sprintf("%s:%d", download.UUID, file.ID)
			sig := crypto.GenerateSignature(input, expires, []byte("download_secret"))
			signedURL := fmt.Sprintf("/api/utilities/downloads/%s?expires=%d&sig=%s&id=%d", download.UUID, expires, sig, file.ID)

			c.JSON(http.StatusOK, internal.APIResponse[string]{
				Status:  "success",
				Message: "signed_url_generated",
				Error:   "",
				Data:    signedURL,
			})
		} else if download.Type == "http" {
			input := fmt.Sprintf("%s:%d", download.UUID, download.ID)
			sig := crypto.GenerateSignature(input, expires, []byte("download_secret"))
			signedURL := fmt.Sprintf("/api/utilities/downloads/%s?expires=%d&sig=%s&id=%d", download.UUID, expires, sig, download.ID)

			c.JSON(http.StatusOK, internal.APIResponse[string]{
				Status:  "success",
				Message: "signed_url_generated",
				Error:   "",
				Data:    signedURL,
			})
		}
	}
}

// @Summary Download File
// @Description Download a file from a signed URL
// @Tags Utilities
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param uuid path string true "Download UUID"
// @Param expires query int true "Expiration time in Unix timestamp"
// @Param sig query string true "Signature"
// @Success 200 {file} file "File Download"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /utilities/downloads/{uuid} [get]
func DownloadFileFromSignedURL(utilitiesService *utilities.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		uuid := c.Param("uuid")
		expiresStr := c.Query("expires")
		sig := c.Query("sig")
		idStr := c.Query("id")

		if uuid == "" || expiresStr == "" || sig == "" || idStr == "" {
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

		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_file_id",
			})
			return
		}

		input := fmt.Sprintf("%s:%d", uuid, id)
		expectedSig := crypto.GenerateSignature(input, expires, []byte("download_secret"))
		if sig != expectedSig {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "signature_mismatch",
			})
			return
		}

		filePath, err := utilitiesService.GetFilePathById(uuid, id)
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

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/services/system"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/semaphore"
)

type AddFileOrFolderRequest struct {
	Path     string `json:"path" binding:"required"`
	Name     string `json:"name" binding:"required"`
	IsFolder *bool  `json:"isFolder" binding:"required"`
}

type RenameFileOrFolderRequest struct {
	ID      string `json:"id" binding:"required"`
	NewName string `json:"newName" binding:"required"`
}

type CopyOrMoveFileOrFolderRequest struct {
	ID      string `json:"id" binding:"required"`
	NewPath string `json:"newPath" binding:"required"`
	Cut     *bool  `json:"cut" binding:"required"`
}

type DeleteFilesOrFoldersRequest struct {
	Paths []string `json:"paths" binding:"required"`
}

type CopyOrMoveFilesOrFoldersRequest struct {
	Pairs [][2]string `json:"pairs" binding:"required"`
	Cut   *bool       `json:"cut" binding:"required"`
}

type DeleteUploadRequest struct {
	Data struct {
		UploadID string `json:"uploadId"`
	} `json:"data"`
}

type UploadFileResponse struct {
	Path     string `json:"path"`
	UploadID string `json:"uploadId"`
	Bytes    int64  `json:"bytes"`
}

// @Summary Find Files on System
// @Description Find files on the system based on a search term
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Query id string "Search term"
// @Success 200 {object} internal.APIResponse[[]systemServiceInterfaces.FileNode]
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer [get]
func Files(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Query("id")
		nodes, err := systemService.Traverse(id)

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]systemServiceInterfaces.FileNode]{
			Status:  "success",
			Message: "files_listed",
			Error:   "",
			Data:    nodes,
		})
	}
}

// @Summary Add File or Folder
// @Description Add a file or folder to the system
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body AddFileOrFolderRequest true "Request body"
// @Success 200 {object} internal.APIResponse[any]
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer [post]
func AddFileOrFolder(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request AddFileOrFolderRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		var isFolder bool

		if request.IsFolder != nil {
			isFolder = *request.IsFolder
		} else {
			isFolder = false
		}

		err := systemService.AddFileOrFolder(request.Path, request.Name, isFolder)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "file_or_folder_added",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete File or Folder
// @Description Delete a file or folder from the system
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id query string true "Path to the file or folder"
// @Success 200 {object} internal.APIResponse[any]
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer [delete]
func DeleteFileOrFolder(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Query("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   "id is required",
				Data:    nil,
			})
			return
		}

		err := systemService.DeleteFileOrFolder(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "file_or_folder_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete Files or Folders
// @Description Delete multiple files or folders from the system
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Request body DeleteFilesOrFoldersRequest true "Delete Files or Folders Request"
// @Success 200 {object} internal.APIResponse[any]
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer/delete [post]
func DeleteFilesOrFolders(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request DeleteFilesOrFoldersRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if len(request.Paths) == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   "no paths provided",
				Data:    nil,
			})
			return
		}

		err := systemService.DeleteFilesOrFolders(request.Paths)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "files_or_folders_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Rename File or Folder
// @Description Rename a file or folder in the system
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id query string true "Path to the file or folder"
// @Request body RenameFileOrFolderRequest true "Rename File or Folder Request"
// @Success 200 {object} internal.APIResponse[any]
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer/rename [post]
func RenameFileOrFolder(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request RenameFileOrFolderRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if request.ID == "" || request.NewName == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   "id and new name are required",
				Data:    nil,
			})
			return
		}

		err := systemService.RenameFileOrFolder(request.ID, request.NewName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "file_or_folder_renamed",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Download File
// @Description Download a file from the system
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id query string true "Path to the file"
// @Success 200 {file} file "File content"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer/download [get]
func DownloadFile(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Query("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   "id is required",
				Data:    nil,
			})
			return
		}

		file, err := systemService.DownloadFile(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.Header("Content-Disposition", "attachment; filename="+filepath.Base(id))
		c.Header("Content-Type", "application/octet-stream")
		c.File(file)
	}
}

// @Summary Copy or Move File or Folder
// @Description Copy or move a file or folder to a new path
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CopyOrMoveFileOrFolderRequest true "Request body"
// @Success 200 {object} internal.APIResponse[any]
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer/copy-or-move [post]
func CopyOrMoveFileOrFolder(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CopyOrMoveFileOrFolderRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if request.ID == "" || request.NewPath == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   "id and new path are required",
				Data:    nil,
			})
			return
		}

		move := false
		if request.Cut != nil {
			move = *request.Cut
		}

		if err := systemService.CopyOrMoveFileOrFolder(request.ID, request.NewPath, move); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "file_or_folder_copied_or_moved",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Copy or Move Files or Folders
// @Description Copy or move multiple files or folders to new paths
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Request body CopyOrMoveFilesOrFoldersRequest true "Copy or Move Files or Folders Request"
// @Success 200 {object} internal.APIResponse[any]
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer/copy-or-move-batch [post]
func CopyOrMoveFilesOrFolders(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CopyOrMoveFilesOrFoldersRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if len(request.Pairs) == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "bad_request",
				Error:   "no pairs provided",
				Data:    nil,
			})
			return
		}

		move := false
		if request.Cut != nil {
			move = *request.Cut
		}

		err := systemService.CopyOrMoveFilesOrFolders(request.Pairs, move)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "internal_server_error",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "files_or_folders_copied_or_moved",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Upload File (FilePond)
// @Description Upload a file to a user-specified path via FilePond; fails if file exists
// @Tags System
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param path query string true "Destination folder path (e.g. /zroot/share)"
// @Param filepond formData file true "File to upload"
// @Success 200 {object} internal.APIResponse[UploadFileResponse]
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 403 {object} internal.APIResponse[any] "Destination Permission Denied"
// @Failure 408 {object} internal.APIResponse[any] "Upload Cancelled"
// @Failure 413 {object} internal.APIResponse[any] "Upload Too Large"
// @Failure 409 {object} internal.APIResponse[any] "Conflict - File Already Exists"
// @Failure 429 {object} internal.APIResponse[any] "Upload Capacity Exhausted"
// @Failure 507 {object} internal.APIResponse[any] "Insufficient Storage"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer/upload [post]
func UploadFile(
	systemService *system.Service,
	admission *semaphore.Weighted,
) gin.HandlerFunc {
	return newFileExplorerUploadHandler(
		systemService,
		configuredFileExplorerUploadPolicy(),
		admission,
	)
}

// @Summary Delete Uploaded File
// @Description Deletes a previously uploaded file using a server-issued upload identity
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body DeleteUploadRequest true "Delete Upload Request"
// @Success 200 {object} internal.APIResponse[any]
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "File Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer/upload [delete]
func DeleteUpload(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req DeleteUploadRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_json",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if strings.TrimSpace(req.Data.UploadID) == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_upload_id",
				Error:   "data.uploadId is required",
				Data:    nil,
			})
			return
		}

		err := systemService.DeleteFileExplorerUpload(req.Data.UploadID, c.GetUint("UserID"))
		if err != nil {
			switch {
			case errors.Is(err, system.ErrFileExplorerUploadNotFound):
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "upload_not_found",
					Error:   "upload identity was not found",
					Data:    nil,
				})
			case errors.Is(err, system.ErrFileExplorerUploadNotFile):
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status: "error", Message: "not_a_file", Error: err.Error(), Data: nil,
				})
			case strings.Contains(err.Error(), "restore_in_progress"):
				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status: "error", Message: "restore_in_progress", Error: err.Error(), Data: nil,
				})
			default:
				c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
					Status: "error", Message: "delete_failed", Error: err.Error(), Data: nil,
				})
			}
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "file_deleted",
			Error:   "",
			Data:    nil,
		})
	}
}

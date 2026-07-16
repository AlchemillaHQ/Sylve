// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/alchemillahq/sylve/internal"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
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
		Path string `json:"path"`
	} `json:"data"`
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
// @Success 200 {object} internal.APIResponse[map[string]string]
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 409 {object} internal.APIResponse[any] "Conflict - File Already Exists"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer/upload [post]
func UploadFile(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawDestPath := c.Query("path")
		if rawDestPath == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_path",
				Error:   "path query parameter is required",
				Data:    nil,
			})
			return
		}

		destPath, err := url.PathUnescape(rawDestPath)
		if err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_path_encoding",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if err := c.Request.ParseMultipartForm(100 << 20); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "parse_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		files := c.Request.MultipartForm.File["filepond"]
		var fileHeader *multipart.FileHeader
		for _, f := range files {
			if f.Filename != "" {
				fileHeader = f
				break
			}
		}

		if fileHeader == nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_file",
				Error:   "no file found in filepond field",
				Data:    nil,
			})
			return
		}

		tempPath := filepath.Join(os.TempDir(), fileHeader.Filename)
		if err := c.SaveUploadedFile(fileHeader, tempPath); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "temp_save_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		defer os.Remove(tempPath)

		finalPath := filepath.Join(destPath, filepath.Base(fileHeader.Filename))
		if err := systemService.EnsureFileExplorerMutationAllowed(finalPath); err != nil {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status: "error", Message: "restore_in_progress", Error: err.Error(), Data: nil,
			})
			return
		}

		if _, err := os.Stat(finalPath); err == nil {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status:  "error",
				Message: "file_exists",
				Error:   "file already exists at destination",
				Data:    nil,
			})
			return
		}

		if err := utils.CopyFile(tempPath, finalPath); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "copy_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[map[string]string]{
			Status:  "success",
			Message: "file_uploaded",
			Error:   "",
			Data: map[string]string{
				"path": finalPath,
			},
		})
	}
}

// @Summary Delete Uploaded File
// @Description Deletes a previously uploaded file using JSON body metadata
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param path query string true "Parent folder path (unused in deletion, just for consistency)"
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

		if req.Data.Path == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_file_path",
				Error:   "data.path is required",
				Data:    nil,
			})
			return
		}

		path := req.Data.Path
		if err := systemService.EnsureFileExplorerMutationAllowed(path); err != nil {
			c.JSON(http.StatusConflict, internal.APIResponse[any]{
				Status: "error", Message: "restore_in_progress", Error: err.Error(), Data: nil,
			})
			return
		}

		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "file_not_found",
					Error:   "file does not exist",
					Data:    nil,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "stat_failed",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}

		if info.IsDir() {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "not_a_file",
				Error:   "cannot delete a directory",
				Data:    nil,
			})
			return
		}

		if err := os.Remove(path); err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "delete_failed",
				Error:   err.Error(),
				Data:    nil,
			})
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

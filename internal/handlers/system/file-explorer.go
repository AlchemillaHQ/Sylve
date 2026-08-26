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
	"mime"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/logger"
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

type DeleteFilesOrFoldersRequest struct {
	Paths []string `json:"paths" binding:"required"`
}

type CopyOrMoveFilesOrFoldersRequest struct {
	Items []systemServiceInterfaces.FileTransferItem `json:"items" binding:"required"`
	Move  bool                                       `json:"move"`
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

func bindFileExplorerJSON(c *gin.Context, target any) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		status := http.StatusBadRequest
		code := "invalid_file_explorer_request"
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			status = http.StatusRequestEntityTooLarge
			code = "file_explorer_request_too_large"
		}
		c.JSON(status, internal.APIResponse[any]{
			Status:  "error",
			Message: code,
			Error:   code,
			Data:    nil,
		})
		return false
	}
	return true
}

func fileExplorerErrorStatus(err error) int {
	switch {
	case errors.Is(err, system.ErrFileExplorerInvalidPath),
		errors.Is(err, system.ErrFileExplorerInvalidName),
		errors.Is(err, system.ErrFileExplorerRootMutation),
		errors.Is(err, system.ErrFileExplorerNotDirectory),
		errors.Is(err, system.ErrFileExplorerInvalidOperation),
		errors.Is(err, system.ErrFileExplorerUnsupportedType),
		errors.Is(err, system.ErrFileExplorerBatchTooLarge):
		return http.StatusBadRequest
	case errors.Is(err, system.ErrFileExplorerPermissionDenied):
		return http.StatusForbidden
	case errors.Is(err, system.ErrFileExplorerNotFound):
		return http.StatusNotFound
	case errors.Is(err, system.ErrFileExplorerAlreadyExists),
		errors.Is(err, system.ErrFileExplorerBatchConflict),
		errors.Is(err, system.ErrFileExplorerRestoreInProgress):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func writeFileExplorerError(c *gin.Context, err error) {
	status := fileExplorerErrorStatus(err)
	code := system.FileExplorerErrorCode(err)
	if status == http.StatusInternalServerError {
		logger.L.Error().Msgf("File Explorer operation failed: %v", err)
	}
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: code,
		Error:   code,
		Data:    nil,
	})
}

// @Summary Browse Files and Folders
// @Description List the direct entries in an absolute directory path
// @Tags System
// @Produce json
// @Security BearerAuth
// @Param id query string false "Absolute directory path; defaults to /"
// @Success 200 {object} internal.APIResponse[[]systemServiceInterfaces.FileNode] "Files and folders listed"
// @Failure 400 {object} internal.APIResponse[any] "Invalid directory path"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access or filesystem permission required"
// @Failure 404 {object} internal.APIResponse[any] "Directory not found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer [get]
func Files(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Query("id")
		nodes, err := systemService.Traverse(id)

		if err != nil {
			writeFileExplorerError(c, err)
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

// @Summary Create a File or Folder
// @Description Create a new file or folder in an absolute directory path without overwriting an existing entry
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body AddFileOrFolderRequest true "Request body"
// @Success 201 {object} internal.APIResponse[any] "File or folder created"
// @Failure 400 {object} internal.APIResponse[any] "Invalid request, path, or name"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access or filesystem permission required"
// @Failure 404 {object} internal.APIResponse[any] "Parent directory not found"
// @Failure 409 {object} internal.APIResponse[any] "Entry already exists or restore is in progress"
// @Failure 413 {object} internal.APIResponse[any] "Request body too large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer [post]
func AddFileOrFolder(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request AddFileOrFolderRequest
		if !bindFileExplorerJSON(c, &request) {
			return
		}

		err := systemService.AddFileOrFolder(request.Path, request.Name, *request.IsFolder)
		if err != nil {
			writeFileExplorerError(c, err)
			return
		}

		c.JSON(http.StatusCreated, internal.APIResponse[any]{
			Status:  "success",
			Message: "file_or_folder_added",
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Delete Files or Folders
// @Description Delete one or more files, folders, or symbolic links after validating the complete bounded request
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body DeleteFilesOrFoldersRequest true "Delete files or folders request"
// @Success 200 {object} internal.APIResponse[any] "Files or folders deleted"
// @Failure 400 {object} internal.APIResponse[any] "Invalid, empty, root, or oversized path list"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access or filesystem permission required"
// @Failure 404 {object} internal.APIResponse[any] "Entry not found"
// @Failure 409 {object} internal.APIResponse[any] "Restore is in progress"
// @Failure 413 {object} internal.APIResponse[any] "Request body too large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer/delete [post]
func DeleteFilesOrFolders(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request DeleteFilesOrFoldersRequest
		if !bindFileExplorerJSON(c, &request) {
			return
		}

		err := systemService.DeleteFilesOrFolders(request.Paths)
		if err != nil {
			writeFileExplorerError(c, err)
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
// @Description Rename a file, folder, or symbolic link without overwriting an existing entry
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body RenameFileOrFolderRequest true "Rename file or folder request"
// @Success 200 {object} internal.APIResponse[any] "File or folder renamed"
// @Failure 400 {object} internal.APIResponse[any] "Invalid path or name"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access or filesystem permission required"
// @Failure 404 {object} internal.APIResponse[any] "Entry not found"
// @Failure 409 {object} internal.APIResponse[any] "Destination exists or restore is in progress"
// @Failure 413 {object} internal.APIResponse[any] "Request body too large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer/rename [post]
func RenameFileOrFolder(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request RenameFileOrFolderRequest
		if !bindFileExplorerJSON(c, &request) {
			return
		}

		err := systemService.RenameFileOrFolder(request.ID, request.NewName)
		if err != nil {
			writeFileExplorerError(c, err)
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

// @Summary Download a file
// @Description Stream a regular file from an absolute system path with HTTP range support
// @Tags System
// @Produce octet-stream
// @Security BearerAuth
// @Param id query string true "Absolute path to the regular file"
// @Param hash query string false "SHA-256 token hash alternative to Bearer authentication"
// @Param auth query string false "Hex-encoded selected-node routing payload used by browser downloads"
// @Success 200 {file} file "File content"
// @Success 206 {file} file "Partial file content"
// @Failure 400 {object} internal.APIResponse[any] "Invalid path or non-regular file"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access or filesystem permission required"
// @Failure 404 {object} internal.APIResponse[any] "File not found"
// @Failure 416 {string} string "Range Not Satisfiable"
// @Failure 500 {object} internal.APIResponse[any] "Unexpected filesystem failure"
// @Router /system/file-explorer/download [get]
func DownloadFile(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Query("id")
		if id == "" {
			writeFileExplorerError(c, system.ErrFileExplorerInvalidPath)
			return
		}

		download, err := systemService.DownloadFile(id)
		if err != nil {
			writeFileExplorerError(c, err)
			return
		}
		defer download.Reader.Close()

		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": download.Name})
		if disposition == "" {
			disposition = `attachment; filename="download"`
		}
		c.Header("Content-Disposition", disposition)
		c.Header("Content-Type", "application/octet-stream")
		http.ServeContent(c.Writer, c.Request, download.Name, download.ModTime, download.Reader)
	}
}

// @Summary Copy or Move Files or Folders
// @Description Copy or move up to 512 regular files, folders, or symbolic links without overwriting existing entries; an existing destination directory receives the source basename
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CopyOrMoveFilesOrFoldersRequest true "Copy or move files or folders request"
// @Success 200 {object} internal.APIResponse[any] "Files or folders copied or moved"
// @Failure 400 {object} internal.APIResponse[any] "Invalid path, operation, file type, or oversized item list"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access or filesystem permission required"
// @Failure 404 {object} internal.APIResponse[any] "Source or destination parent not found"
// @Failure 409 {object} internal.APIResponse[any] "Destination exists, batch paths conflict, or restore is in progress"
// @Failure 413 {object} internal.APIResponse[any] "Request body too large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer/copy-or-move-batch [post]
func CopyOrMoveFilesOrFolders(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CopyOrMoveFilesOrFoldersRequest
		if !bindFileExplorerJSON(c, &request) {
			return
		}

		err := systemService.CopyOrMoveFilesOrFolders(request.Items, request.Move)
		if err != nil {
			writeFileExplorerError(c, err)
			return
		}

		message := "files_or_folders_copied"
		if request.Move {
			message = "files_or_folders_moved"
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: message,
			Error:   "",
			Data:    nil,
		})
	}
}

// @Summary Upload a File
// @Description Upload exactly one file to an absolute destination folder through FilePond without overwriting an existing entry
// @Tags System
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param path query string true "Destination folder path (e.g. /zroot/share)"
// @Param filepond formData file true "File to upload"
// @Success 201 {object} internal.APIResponse[UploadFileResponse] "File uploaded"
// @Failure 400 {object} internal.APIResponse[any] "Invalid path, filename, or multipart request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access or destination permission required"
// @Failure 404 {object} internal.APIResponse[any] "Destination folder not found"
// @Failure 408 {object} internal.APIResponse[any] "Upload cancelled or timed out"
// @Failure 409 {object} internal.APIResponse[any] "Destination exists or restore is in progress"
// @Failure 413 {object} internal.APIResponse[any] "File or multipart request too large"
// @Failure 429 {object} internal.APIResponse[any] "Upload capacity exhausted"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 507 {object} internal.APIResponse[any] "Insufficient storage or quota"
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

// @Summary Revert an Uploaded File
// @Description Delete the unchanged file associated with a short-lived, server-issued FilePond upload identity
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body DeleteUploadRequest true "Upload revert request"
// @Success 200 {object} internal.APIResponse[any] "Upload reverted"
// @Failure 400 {object} internal.APIResponse[any] "Invalid request or missing upload identity"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access or filesystem permission required"
// @Failure 404 {object} internal.APIResponse[any] "Upload identity not found, expired, or owned by another user"
// @Failure 409 {object} internal.APIResponse[any] "Filesystem entry changed or restore is in progress"
// @Failure 413 {object} internal.APIResponse[any] "Request body too large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer/upload [delete]
func DeleteUpload(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req DeleteUploadRequest
		if !bindFileExplorerJSON(c, &req) {
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
			writeDeleteUploadError(c, err)
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

func deleteUploadErrorStatusAndCode(err error) (int, string) {
	switch {
	case errors.Is(err, system.ErrFileExplorerUploadNotFound):
		return http.StatusNotFound, "upload_not_found"
	case errors.Is(err, system.ErrFileExplorerUploadConflict):
		return http.StatusConflict, "upload_conflict"
	case errors.Is(err, system.ErrFileExplorerRestoreInProgress):
		return http.StatusConflict, "restore_in_progress"
	case errors.Is(err, system.ErrFileExplorerPermissionDenied):
		return http.StatusForbidden, system.ErrFileExplorerPermissionDenied.Error()
	default:
		return http.StatusInternalServerError, "upload_delete_failed"
	}
}

func writeDeleteUploadError(c *gin.Context, err error) {
	status, code := deleteUploadErrorStatusAndCode(err)
	if status == http.StatusInternalServerError {
		logger.L.Error().Msgf("File Explorer upload revert failed: %v", err)
	}
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: code,
		Error:   code,
		Data:    nil,
	})
}

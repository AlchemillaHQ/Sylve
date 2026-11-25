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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/logger"
	authSvc "github.com/alchemillahq/sylve/internal/services/auth"
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

type DownloadTicketRequest struct {
	Kind string   `json:"kind" binding:"required,oneof=file archive"`
	ID   string   `json:"id"`
	IDs  []string `json:"ids"`
}

func (r DownloadTicketRequest) paths() ([]string, bool) {
	ids := r.IDs
	if r.ID != "" {
		if len(ids) > 0 {
			return nil, false
		}
		ids = []string{r.ID}
	}
	if len(ids) == 0 || len(ids) > system.MaxFileExplorerBatchItems || (r.Kind == "file" && len(ids) != 1) {
		return nil, false
	}
	return ids, !slices.Contains(ids, "")
}

type DownloadTicketResponse struct {
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expiresAt"`
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
	case errors.Is(err, system.ErrFileExplorerMoveIncomplete):
		return http.StatusInternalServerError
	case errors.Is(err, system.ErrFileExplorerInvalidPath),
		errors.Is(err, system.ErrFileExplorerInvalidName),
		errors.Is(err, system.ErrFileExplorerNameTooLong),
		errors.Is(err, system.ErrFileExplorerRootMutation),
		errors.Is(err, system.ErrFileExplorerNotDirectory),
		errors.Is(err, system.ErrFileExplorerInvalidOperation),
		errors.Is(err, system.ErrFileExplorerUnsupportedType),
		errors.Is(err, system.ErrFileExplorerBatchTooLarge):
		return http.StatusBadRequest
	case errors.Is(err, system.ErrFileExplorerPermissionDenied),
		errors.Is(err, system.ErrFileExplorerProtectedPath):
		return http.StatusForbidden
	case errors.Is(err, system.ErrFileExplorerNotFound):
		return http.StatusNotFound
	case errors.Is(err, system.ErrFileExplorerAlreadyExists),
		errors.Is(err, system.ErrFileExplorerBatchConflict),
		errors.Is(err, system.ErrFileExplorerRestoreInProgress),
		errors.Is(err, system.ErrFileExplorerBusy),
		errors.Is(err, system.ErrFileExplorerCancelled):
		return http.StatusConflict
	case errors.Is(err, system.ErrFileExplorerNoSpace):
		return http.StatusInsufficientStorage
	default:
		return http.StatusInternalServerError
	}
}

type FileExplorerErrorData struct {
	Path      string   `json:"path,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Completed []string `json:"completed,omitempty"`
}

func writeFileExplorerError(c *gin.Context, err error) {
	status := fileExplorerErrorStatus(err)
	code := system.FileExplorerErrorCode(err)
	if status == http.StatusInternalServerError {
		logger.L.Error().Msgf("File Explorer operation failed: %v", err)
	}
	var details FileExplorerErrorData
	var protected *system.FileExplorerProtectedError
	if errors.As(err, &protected) {
		details.Path = protected.Path
		details.Reason = protected.Reason
	}
	var batch *system.FileExplorerBatchError
	if errors.As(err, &batch) {
		details.Completed = batch.Completed
	}
	var data any
	if details.Path != "" || len(details.Completed) > 0 {
		data = details
	}
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: code,
		Error:   code,
		Data:    data,
	})
}

// @Summary Browse Files and Folders
// @Description List the direct entries in an absolute directory path. Symbolic links report their target in link and take the type of what they point to; broken links are marked broken. Entries include their mode, owner and group, the file system mounted there (mount), and why delete, rename and move refuse them (protected).
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

// @Summary Create a Download Ticket
// @Description Issue a single-use ticket, valid for one minute, that authenticates one GET of /system/file-explorer/download (kind file) or /system/file-explorer/archive (kind archive) for exactly the given id, or for exactly the given ids in that order (kind archive, up to 512). Pass it as the ticket query parameter so no login token appears in the URL. Tickets are kept on the node that issues them, so send the download through that node, using /nodes/{node}/... for another cluster member.
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body DownloadTicketRequest true "Download ticket request"
// @Success 200 {object} internal.APIResponse[DownloadTicketResponse] "Ticket issued"
// @Failure 400 {object} internal.APIResponse[any] "Invalid request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access and a local session required"
// @Failure 429 {object} internal.APIResponse[any] "Too many outstanding tickets"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /auth/download-tickets [post]
func CreateDownloadTicket(authService *authSvc.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		var request DownloadTicketRequest
		if !bindFileExplorerJSON(c, &request) {
			return
		}
		ids, ok := request.paths()
		if !ok {
			writeFileExplorerError(c, system.ErrFileExplorerInvalidPath)
			return
		}
		ticket, expires, err := authService.IssueDownloadTicket(
			c.GetString("Token"),
			middleware.FileExplorerTicketScope(request.Kind, ids...),
		)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, authSvc.ErrDownloadTicketCapacity) {
				status = http.StatusTooManyRequests
			}
			code := "download_ticket_failed"
			if status == http.StatusTooManyRequests {
				code = authSvc.ErrDownloadTicketCapacity.Error()
			}
			c.JSON(status, internal.APIResponse[any]{Status: "error", Message: code, Error: code})
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[DownloadTicketResponse]{
			Status:  "success",
			Message: "download_ticket_created",
			Data:    DownloadTicketResponse{Ticket: ticket, ExpiresAt: expires},
		})
	}
}

// @Summary List a Folder
// @Description List an absolute directory path, returning at most limit entries (default 10000, maximum 50000). When q is set, only entries whose names contain it (case-insensitive) are returned. total counts every entry, matched counts the entries that match q, and truncated is set when matched exceeds limit; the returned entries are then the first by folders first and name.
// @Tags System
// @Produce json
// @Security BearerAuth
// @Param id query string false "Absolute directory path; defaults to /"
// @Param limit query int false "Maximum entries to return"
// @Param q query string false "Case-insensitive name filter"
// @Success 200 {object} internal.APIResponse[systemServiceInterfaces.FileListing] "Folder listed"
// @Failure 400 {object} internal.APIResponse[any] "Invalid directory path"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access or filesystem permission required"
// @Failure 404 {object} internal.APIResponse[any] "Directory not found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer/list [get]
func ListFolder(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit, err := strconv.Atoi(c.DefaultQuery("limit", "0"))
		if err != nil || limit < 0 {
			writeFileExplorerError(c, system.ErrFileExplorerInvalidOperation)
			return
		}
		listing, err := systemService.ListDirectory(c.Query("id"), limit, c.Query("q"))
		if err != nil {
			writeFileExplorerError(c, err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[systemServiceInterfaces.FileListing]{
			Status:  "success",
			Message: "folder_listed",
			Error:   "",
			Data:    listing,
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
// @Failure 507 {object} internal.APIResponse[any] "Insufficient storage or quota"
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
// @Description Delete one or more files, folders, or symbolic links after validating the complete bounded request. Protected paths (/, top-level entries, the base system, mount points and anything containing one, jail roots and Sylve data) are refused with 403 and data {path, reason}. A refusal deletes nothing. If deleting stops part-way, data.completed lists the paths already deleted. Closing the request stops a delete in progress: whatever was not deleted yet is kept and file_explorer_cancelled is returned. Symbolic links are removed, never followed, and deleting never descends into another mounted file system; a mount found inside a folder stops the delete with 403 and reason mount_point.
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body DeleteFilesOrFoldersRequest true "Delete files or folders request"
// @Success 200 {object} internal.APIResponse[any] "Files or folders deleted"
// @Failure 400 {object} internal.APIResponse[any] "Invalid, empty, root, or oversized path list"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access or filesystem permission required, or a protected path"
// @Failure 404 {object} internal.APIResponse[any] "Entry not found"
// @Failure 409 {object} internal.APIResponse[any] "Restore is in progress, the entry is busy, or the request was cancelled"
// @Failure 413 {object} internal.APIResponse[any] "Request body too large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /system/file-explorer/delete [post]
func DeleteFilesOrFolders(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request DeleteFilesOrFoldersRequest
		if !bindFileExplorerJSON(c, &request) {
			return
		}

		err := systemService.DeleteFilesOrFolders(c.Request.Context(), request.Paths)
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
// @Description Rename a file, folder, or symbolic link without overwriting an existing entry. Protected paths (/, top-level entries, the base system, mount points and anything containing one, jail roots and Sylve data) are refused with 403 and data {path, reason}.
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body RenameFileOrFolderRequest true "Rename file or folder request"
// @Success 200 {object} internal.APIResponse[any] "File or folder renamed"
// @Failure 400 {object} internal.APIResponse[any] "Invalid path or name"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access or filesystem permission required, or a protected path"
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
// @Description Stream a regular file from an absolute system path with HTTP range support. Browsers authenticate with a single-use ticket from POST /auth/download-tickets (kind file) instead of a login token.
// @Tags System
// @Produce octet-stream
// @Security BearerAuth
// @Param id query string true "Absolute path to the regular file"
// @Param ticket query string false "Single-use ticket from POST /auth/download-tickets"
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

// @Summary Download a Folder or Several Items as Zip
// @Description Stream a zip archive, built while it downloads. With one id, the id must be a folder and the archive holds that folder and everything inside it. With 2 to 512 ids, all in the same folder, the archive holds those items at its top level and is named after the folder and the item count, like "photos - 3 items.zip". Symbolic links are stored as links, not followed. FIFOs, sockets, devices and unreadable items are skipped and listed in SYLVE-SKIPPED.txt inside the archive, next to the archived items. Browsers authenticate with a ticket from POST /auth/download-tickets (kind archive) issued for the same ids in the same order.
// @Tags System
// @Produce application/zip
// @Security BearerAuth
// @Param id query []string true "Absolute path; repeat for several items in one folder" collectionFormat(multi)
// @Param ticket query string false "Single-use ticket from POST /auth/download-tickets"
// @Success 200 {file} file "Zip archive"
// @Failure 400 {object} internal.APIResponse[any] "Invalid path, a single id that is not a folder, or more than 512 ids"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access or filesystem permission required"
// @Failure 404 {object} internal.APIResponse[any] "Item not found"
// @Failure 409 {object} internal.APIResponse[any] "The ids are not in the same folder or repeat a path"
// @Failure 500 {object} internal.APIResponse[any] "Unexpected filesystem failure"
// @Router /system/file-explorer/archive [get]
func DownloadFolder(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		archive, err := systemService.PrepareArchive(c.QueryArray("id"))
		if err != nil {
			writeFileExplorerError(c, err)
			return
		}
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": archive.Name + ".zip"})
		if disposition == "" {
			disposition = `attachment; filename="folder.zip"`
		}
		c.Header("Content-Disposition", disposition)
		c.Header("Content-Type", "application/zip")
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Status(http.StatusOK)
		if err := system.WriteArchive(c.Request.Context(), archive, c.Writer); err != nil &&
			!errors.Is(err, system.ErrFileExplorerCancelled) {
			logger.L.Warn().Err(err).Str("archive", archive.Name).Msg("File Explorer archive stopped")
		}
	}
}

// @Summary Copy or Move Files or Folders
// @Description Copy or move up to 512 regular files, folders, or symbolic links without overwriting existing entries; an existing destination directory receives the source basename. Copies keep owner, group, mode, modification time, ACLs, user extended attributes and file flags, and files hard-linked to each other stay hard-linked within one request. Moving a protected path (/, top-level entries, the base system, mount points and anything containing one, jail roots and Sylve data) is refused with 403 and data {path, reason}. If a move across file systems copies everything but cannot remove all of the source, the copy is kept and file_explorer_move_incomplete is returned. Closing the request cancels a copy in progress: the partly copied item is removed and file_explorer_cancelled is returned. If a batch stops part-way, data.completed lists the sources already copied or moved.
// @Tags System
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CopyOrMoveFilesOrFoldersRequest true "Copy or move files or folders request"
// @Success 200 {object} internal.APIResponse[any] "Files or folders copied or moved"
// @Failure 400 {object} internal.APIResponse[any] "Invalid path, operation, file type, or oversized item list"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access or filesystem permission required, or a protected path"
// @Failure 404 {object} internal.APIResponse[any] "Source or destination parent not found"
// @Failure 409 {object} internal.APIResponse[any] "Destination exists, batch paths conflict, restore is in progress, or the request was cancelled"
// @Failure 413 {object} internal.APIResponse[any] "Request body too large"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 507 {object} internal.APIResponse[any] "Insufficient storage or quota"
// @Router /system/file-explorer/copy-or-move-batch [post]
func CopyOrMoveFilesOrFolders(systemService *system.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request CopyOrMoveFilesOrFoldersRequest
		if !bindFileExplorerJSON(c, &request) {
			return
		}

		err := systemService.CopyOrMoveFilesOrFolders(c.Request.Context(), request.Items, request.Move)
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
// @Description Upload exactly one file to an absolute destination folder through FilePond without overwriting an existing entry. The uploaded file gets mode 0644. Error responses carry data {code, retryable, limitBytes}, and server failures report only the code.
// @Tags System
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param path query string true "Destination folder path (e.g. /zroot/share)"
// @Param filepond formData file true "File to upload"
// @Success 201 {object} internal.APIResponse[UploadFileResponse] "File uploaded"
// @Failure 400 {object} internal.APIResponse[FileExplorerUploadErrorData] "Invalid path, filename, or multipart request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Administrator access or destination permission required"
// @Failure 404 {object} internal.APIResponse[FileExplorerUploadErrorData] "Destination folder not found"
// @Failure 408 {object} internal.APIResponse[FileExplorerUploadErrorData] "Upload cancelled or timed out"
// @Failure 409 {object} internal.APIResponse[FileExplorerUploadErrorData] "Destination exists or restore is in progress"
// @Failure 413 {object} internal.APIResponse[FileExplorerUploadErrorData] "File or multipart request too large"
// @Failure 429 {object} internal.APIResponse[FileExplorerUploadErrorData] "Upload capacity exhausted"
// @Failure 500 {object} internal.APIResponse[FileExplorerUploadErrorData] "Internal Server Error"
// @Failure 507 {object} internal.APIResponse[FileExplorerUploadErrorData] "Insufficient storage or quota"
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

		err := systemService.DeleteFileExplorerUpload(req.Data.UploadID, c.GetString("Owner"))
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

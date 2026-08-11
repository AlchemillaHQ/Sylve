// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func TestAddFileOrFolderAcceptsExplicitFalseAndReturnsCreated(t *testing.T) {
	root := t.TempDir()
	body, err := json.Marshal(AddFileOrFolderRequest{
		Path:     root,
		Name:     "new-file",
		IsFolder: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/system/file-explorer", AddFileOrFolder(&system.Service{}))
	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPost,
		"/system/file-explorer",
		body,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d; want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}
	info, err := os.Stat(filepath.Join(root, "new-file"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("created mode = %s; want regular file", info.Mode())
	}
}

func TestFileExplorerBrowseReturnsEmptyArray(t *testing.T) {
	root := t.TempDir()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/system/file-explorer", Files(&system.Service{}))
	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodGet,
		"/system/file-explorer?id="+url.QueryEscape(root),
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	body := testutil.DecodeJSONResponse[internal.APIResponse[[]any]](t, response)
	if body.Data == nil || len(body.Data) != 0 {
		t.Fatalf("data = %#v; want non-nil empty array", body.Data)
	}
}

func TestFileExplorerDownloadStreamsRangesAndFormatsFilename(t *testing.T) {
	root := t.TempDir()
	filename := `archive "α"; final.iso`
	path := filepath.Join(root, filename)
	contents := []byte("0123456789")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/system/file-explorer/download", DownloadFile(&system.Service{}))
	endpoint := "/api/system/file-explorer/download?id=" + url.QueryEscape(path)

	response := testutil.PerformRequest(t, router, http.MethodGet, endpoint, nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if !strings.EqualFold(response.Header().Get("Content-Type"), "application/octet-stream") {
		t.Fatalf("content type = %q; want application/octet-stream", response.Header().Get("Content-Type"))
	}
	if response.Body.String() != string(contents) {
		t.Fatalf("body = %q; want %q", response.Body.Bytes(), contents)
	}
	mediaType, parameters, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("parse Content-Disposition: %v", err)
	}
	if mediaType != "attachment" || parameters["filename"] != filename {
		t.Fatalf("Content-Disposition = %q; parsed type=%q filename=%q", response.Header().Get("Content-Disposition"), mediaType, parameters["filename"])
	}

	rangeResponse := testutil.PerformRequest(
		t,
		router,
		http.MethodGet,
		endpoint,
		nil,
		map[string]string{"Range": "bytes=2-5"},
	)
	if rangeResponse.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d; want %d; body = %s", rangeResponse.Code, http.StatusPartialContent, rangeResponse.Body.String())
	}
	if rangeResponse.Body.String() != "2345" {
		t.Fatalf("range body = %q; want 2345", rangeResponse.Body.String())
	}
	if got := rangeResponse.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("Content-Range = %q; want bytes 2-5/10", got)
	}

	invalidRangeResponse := testutil.PerformRequest(
		t,
		router,
		http.MethodGet,
		endpoint,
		nil,
		map[string]string{"Range": "bytes=50-60"},
	)
	if invalidRangeResponse.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("invalid range status = %d; want %d", invalidRangeResponse.Code, http.StatusRequestedRangeNotSatisfiable)
	}
}

func TestFileExplorerDownloadMapsPathErrors(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "blocked.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		endpoint   string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing id",
			endpoint:   "/system/file-explorer/download",
			wantStatus: http.StatusBadRequest,
			wantCode:   system.ErrFileExplorerInvalidPath.Error(),
		},
		{
			name:       "relative path",
			endpoint:   "/system/file-explorer/download?id=" + url.QueryEscape("relative.img"),
			wantStatus: http.StatusBadRequest,
			wantCode:   system.ErrFileExplorerInvalidPath.Error(),
		},
		{
			name:       "directory",
			endpoint:   "/system/file-explorer/download?id=" + url.QueryEscape(root),
			wantStatus: http.StatusBadRequest,
			wantCode:   system.ErrFileExplorerUnsupportedType.Error(),
		},
		{
			name:       "fifo",
			endpoint:   "/system/file-explorer/download?id=" + url.QueryEscape(fifo),
			wantStatus: http.StatusBadRequest,
			wantCode:   system.ErrFileExplorerUnsupportedType.Error(),
		},
		{
			name:       "missing file",
			endpoint:   "/system/file-explorer/download?id=" + url.QueryEscape(filepath.Join(root, "missing.img")),
			wantStatus: http.StatusNotFound,
			wantCode:   system.ErrFileExplorerNotFound.Error(),
		},
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/system/file-explorer/download", DownloadFile(&system.Service{}))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := testutil.PerformRequest(t, router, http.MethodGet, test.endpoint, nil, nil)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d; want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
			if body.Error != test.wantCode || body.Message != test.wantCode {
				t.Fatalf("response = %+v; want code %q", body, test.wantCode)
			}
		})
	}
}

func TestFileExplorerDownloadRequiresAuthenticatedCurrentAdmin(t *testing.T) {
	database := testutil.NewSQLiteTestDB(
		t,
		&models.User{},
		&models.Group{},
		&models.Token{},
		&models.SystemSecrets{},
	)
	if err := database.Create(&models.SystemSecrets{Name: "JWTSecret", Data: "test-jwt-secret"}).Error; err != nil {
		t.Fatalf("seed JWT secret: %v", err)
	}
	passwordHash, err := utils.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "download-admin",
		Password: passwordHash,
		Admin:    true,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	auth, ok := authService.NewAuthService(database).(*authService.Service)
	if !ok {
		t.Fatal("unexpected auth service implementation")
	}
	_, token, err := auth.CreateJWT(user.Username, "correct horse battery staple", "sylve", false)
	if err != nil {
		t.Fatalf("create admin token: %v", err)
	}

	path := filepath.Join(t.TempDir(), "authorized.img")
	if err := os.WriteFile(path, []byte("authorized contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/system/file-explorer/download?id=" + url.QueryEscape(path)
	hashEndpoint := endpoint + "&hash=" + url.QueryEscape(utils.SHA256(token, 1))

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.EnsureAuthenticated(auth))
	router.Use(middleware.RequireLocalAdmin(auth))
	router.GET("/api/system/file-explorer/download", DownloadFile(&system.Service{}))

	unauthenticated := testutil.PerformRequest(t, router, http.MethodGet, endpoint, nil, nil)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d; want %d", unauthenticated.Code, http.StatusUnauthorized)
	}
	authorized := testutil.PerformRequest(t, router, http.MethodGet, hashEndpoint, nil, nil)
	if authorized.Code != http.StatusOK || authorized.Body.String() != "authorized contents" {
		t.Fatalf("authorized response=%d body=%q", authorized.Code, authorized.Body.String())
	}

	if err := database.Model(&models.User{}).Where("id = ?", user.ID).Update("admin", false).Error; err != nil {
		t.Fatalf("demote user: %v", err)
	}
	unauthorized := testutil.PerformRequest(t, router, http.MethodGet, hashEndpoint, nil, nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("demoted user status = %d; want %d", unauthorized.Code, http.StatusUnauthorized)
	}
}

func TestFileExplorerJSONValidationAndBodyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.LimitRequestBody(64))
	router.POST("/system/file-explorer", AddFileOrFolder(&system.Service{}))

	tests := []struct {
		name       string
		body       []byte
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing isFolder",
			body:       []byte(`{"path":"/tmp","name":"entry"}`),
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_file_explorer_request",
		},
		{
			name:       "malformed JSON",
			body:       []byte(`{"path":`),
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_file_explorer_request",
		},
		{
			name:       "oversized body",
			body:       []byte(`{"path":"/tmp","name":"entry","isFolder":false,"padding":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}`),
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   "file_explorer_request_too_large",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := testutil.PerformJSONRequest(
				t,
				router,
				http.MethodPost,
				"/system/file-explorer",
				test.body,
			)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d; want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
			if body.Error != test.wantCode || body.Message != test.wantCode {
				t.Fatalf("response = %+v; want code %q", body, test.wantCode)
			}
		})
	}
}

func TestFileExplorerDeleteMapsRootAndMissingPaths(t *testing.T) {
	root := t.TempDir()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/system/file-explorer/delete", DeleteFilesOrFolders(&system.Service{}))

	tests := []struct {
		name       string
		paths      []string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "root",
			paths:      []string{"/tmp/.."},
			wantStatus: http.StatusBadRequest,
			wantCode:   system.ErrFileExplorerRootMutation.Error(),
		},
		{
			name:       "missing",
			paths:      []string{filepath.Join(root, "missing")},
			wantStatus: http.StatusNotFound,
			wantCode:   system.ErrFileExplorerNotFound.Error(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := json.Marshal(DeleteFilesOrFoldersRequest{Paths: test.paths})
			if err != nil {
				t.Fatal(err)
			}
			response := testutil.PerformJSONRequest(
				t,
				router,
				http.MethodPost,
				"/system/file-explorer/delete",
				request,
			)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d; want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
			if body.Error != test.wantCode {
				t.Fatalf("error = %v; want %q", body.Error, test.wantCode)
			}
		})
	}
}

func TestFileExplorerCopyOrMoveUsesTypedBatchBody(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.WriteFile(source, []byte("copy me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(CopyOrMoveFilesOrFoldersRequest{
		Items: []systemServiceInterfaces.FileTransferItem{{
			Source:      source,
			Destination: destination,
		}},
		Move: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/system/file-explorer/copy-or-move-batch", CopyOrMoveFilesOrFolders(&system.Service{}))
	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPost,
		"/system/file-explorer/copy-or-move-batch",
		request,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(destination, filepath.Base(source))); err != nil {
		t.Fatalf("copied target: %v", err)
	}
}

func TestFileExplorerCopyOrMoveRejectsLegacyPairBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/system/file-explorer/copy-or-move-batch", CopyOrMoveFilesOrFolders(&system.Service{}))
	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPost,
		"/system/file-explorer/copy-or-move-batch",
		[]byte(`{"pairs":[["/tmp/source","/tmp"]],"cut":false}`),
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
	if body.Error != "invalid_file_explorer_request" {
		t.Fatalf("error = %v; want invalid_file_explorer_request", body.Error)
	}
}

func TestFileExplorerCopyOrMoveMapsMissingAndConflictingTargets(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name       string
		item       systemServiceInterfaces.FileTransferItem
		wantStatus int
		wantCode   string
	}{
		{
			name: "missing source",
			item: systemServiceInterfaces.FileTransferItem{
				Source:      filepath.Join(root, "missing"),
				Destination: filepath.Join(root, "unused"),
			},
			wantStatus: http.StatusNotFound,
			wantCode:   system.ErrFileExplorerNotFound.Error(),
		},
		{
			name: "existing target",
			item: systemServiceInterfaces.FileTransferItem{
				Source:      source,
				Destination: target,
			},
			wantStatus: http.StatusConflict,
			wantCode:   system.ErrFileExplorerAlreadyExists.Error(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := json.Marshal(CopyOrMoveFilesOrFoldersRequest{
				Items: []systemServiceInterfaces.FileTransferItem{test.item},
			})
			if err != nil {
				t.Fatal(err)
			}

			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.POST("/system/file-explorer/copy-or-move-batch", CopyOrMoveFilesOrFolders(&system.Service{}))
			response := testutil.PerformJSONRequest(
				t,
				router,
				http.MethodPost,
				"/system/file-explorer/copy-or-move-batch",
				request,
			)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d; want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
			if body.Error != test.wantCode {
				t.Fatalf("error = %v; want %q", body.Error, test.wantCode)
			}
		})
	}
}

func TestFileExplorerCopyOrMoveRouteIsBoundedAndSingleRouteIsRemoved(t *testing.T) {
	routes, err := os.ReadFile("../routes.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(routes)
	limitIndex := strings.Index(source, "fileExplorerCore.Use(middleware.LimitRequestBody")
	batchIndex := strings.Index(source, `fileExplorerCore.POST("/copy-or-move-batch"`)
	if limitIndex < 0 || batchIndex < 0 || limitIndex > batchIndex {
		t.Fatalf("bounded core route ordering missing: limit=%d batch=%d", limitIndex, batchIndex)
	}
	if strings.Contains(source, `POST("/copy-or-move"`) {
		t.Fatal("unused single copy-or-move route is still registered")
	}
	if strings.Contains(source, `fileExplorerTransfer.POST("/copy-or-move`) {
		t.Fatal("copy-or-move JSON route must not bypass the bounded core group")
	}
}

func TestFileExplorerErrorStatus(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus int
	}{
		{system.ErrFileExplorerInvalidPath, http.StatusBadRequest},
		{system.ErrFileExplorerInvalidName, http.StatusBadRequest},
		{system.ErrFileExplorerRootMutation, http.StatusBadRequest},
		{system.ErrFileExplorerNotDirectory, http.StatusBadRequest},
		{system.ErrFileExplorerInvalidOperation, http.StatusBadRequest},
		{system.ErrFileExplorerUnsupportedType, http.StatusBadRequest},
		{system.ErrFileExplorerBatchTooLarge, http.StatusBadRequest},
		{system.ErrFileExplorerPermissionDenied, http.StatusForbidden},
		{system.ErrFileExplorerNotFound, http.StatusNotFound},
		{system.ErrFileExplorerAlreadyExists, http.StatusConflict},
		{system.ErrFileExplorerBatchConflict, http.StatusConflict},
		{system.ErrFileExplorerRestoreInProgress, http.StatusConflict},
		{errors.New("internal details"), http.StatusInternalServerError},
	}

	for _, test := range tests {
		if got := fileExplorerErrorStatus(test.err); got != test.wantStatus {
			t.Errorf("fileExplorerErrorStatus(%v) = %d; want %d", test.err, got, test.wantStatus)
		}
	}
}

func boolPointer(value bool) *bool {
	return &value
}

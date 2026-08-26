// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilitiesHandlers

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/semaphore"
)

func newDownloaderUploadTestRouter(
	service *utilities.Service,
	staging string,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		userID, _ := strconv.ParseUint(c.GetHeader("X-Test-User-ID"), 10, 64)
		c.Set("UserID", uint(userID))
		c.Next()
	})
	router.POST("/uploads", newDownloaderUploadHandler(
		service,
		downloaderUploadPolicy{
			maxFileBytes:    4096,
			maxRequestBytes: 8192,
		},
		staging,
		semaphore.NewWeighted(1),
	))
	router.POST("/uploads/:id/complete", CompleteDownloaderUpload(service))
	router.DELETE("/uploads/:id", AbortDownloaderUpload(service))
	return router
}

func newDownloaderMultipartRequest(t *testing.T, filename, content string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(downloaderUploadField, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/uploads", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-Test-User-ID", "7")
	return request
}

func TestDownloaderUploadEndpointReturnsOpaqueReceiptWithoutPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)
	staging := filepath.Join(root, "downloads", "uploads")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "downloads", "path"), 0o700); err != nil {
		t.Fatal(err)
	}
	database := testutil.NewSQLiteTestDB(
		t,
		&utilitiesModels.Upload{},
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	service := &utilities.Service{DB: database}
	router := newDownloaderUploadTestRouter(service, staging)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, newDownloaderMultipartRequest(t, `C:\fakepath\installer.iso`, "image"))
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), staging) || strings.Contains(response.Body.String(), `"path"`) {
		t.Fatalf("response exposed a host path: %s", response.Body.String())
	}

	var payload internal.APIResponse[DownloaderUploadReceipt]
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.UploadID == "" ||
		payload.Data.Name != "installer.iso" ||
		payload.Data.Bytes != int64(len("image")) ||
		payload.Data.Status != utilitiesModels.UploadStatusStaged {
		t.Fatalf("unexpected receipt: %+v", payload.Data)
	}
	if location := response.Header().Get("Location"); location != "/api/utilities/downloader-uploads/"+payload.Data.UploadID {
		t.Fatalf("Location = %q", location)
	}

	var record utilitiesModels.Upload
	if err := database.First(&record, "id = ?", payload.Data.UploadID).Error; err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(record.Path) != staging ||
		filepath.Base(record.Path) != utilities.DownloaderUploadFinalName(payload.Data.UploadID) {
		t.Fatalf("staged path was not server-derived: %q", record.Path)
	}

	abortRequest := httptest.NewRequest(http.MethodDelete, "/uploads/"+payload.Data.UploadID, nil)
	abortRequest.Header.Set("X-Test-User-ID", "7")
	abortResponse := httptest.NewRecorder()
	router.ServeHTTP(abortResponse, abortRequest)
	if abortResponse.Code != http.StatusOK ||
		!strings.Contains(abortResponse.Body.String(), `"status":"aborted"`) {
		t.Fatalf("abort status=%d body=%s", abortResponse.Code, abortResponse.Body.String())
	}
	if _, err := os.Stat(record.Path); !os.IsNotExist(err) {
		t.Fatalf("abort left staged source: %v", err)
	}
}

func TestCompleteDownloaderUploadRejectsTarAndRawCombination(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)
	staging := filepath.Join(root, "downloads", "uploads")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "downloads", "path"), 0o700); err != nil {
		t.Fatal(err)
	}
	database := testutil.NewSQLiteTestDB(
		t,
		&utilitiesModels.Upload{},
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	service := &utilities.Service{DB: database}
	router := newDownloaderUploadTestRouter(service, staging)

	uploadResponse := httptest.NewRecorder()
	router.ServeHTTP(
		uploadResponse,
		newDownloaderMultipartRequest(t, "rootfs.txz", "archive"),
	)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var receipt internal.APIResponse[DownloaderUploadReceipt]
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}

	completeRequest := httptest.NewRequest(
		http.MethodPost,
		"/uploads/"+receipt.Data.UploadID+"/complete",
		bytes.NewBufferString(`{
			"downloadType":"base-rootfs",
			"automaticExtraction":true,
			"automaticRawConversion":true
		}`),
	)
	completeRequest.Header.Set("Content-Type", "application/json")
	completeRequest.Header.Set("X-Test-User-ID", "7")
	completeResponse := httptest.NewRecorder()
	router.ServeHTTP(completeResponse, completeRequest)

	if completeResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf(
			"completion status=%d body=%s",
			completeResponse.Code,
			completeResponse.Body.String(),
		)
	}
	var failure internal.APIResponse[downloaderUploadErrorData]
	if err := json.Unmarshal(completeResponse.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Message != "incompatible_post_processing_options" ||
		failure.Data.Code != "incompatible_post_processing_options" {
		t.Fatalf("unexpected completion error: %+v", failure)
	}

	var upload utilitiesModels.Upload
	if err := database.First(&upload, "id = ?", receipt.Data.UploadID).Error; err != nil {
		t.Fatal(err)
	}
	if upload.Status != utilitiesModels.UploadStatusStaged {
		t.Fatalf("invalid completion changed upload state to %q", upload.Status)
	}
}

func TestAbortDownloaderUploadReportsCompletedIdentityWithoutRemovingIt(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)
	staging := filepath.Join(root, "downloads", "uploads")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "downloads", "path"), 0o700); err != nil {
		t.Fatal(err)
	}
	database := testutil.NewSQLiteTestDB(
		t,
		&utilitiesModels.Upload{},
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	service := &utilities.Service{DB: database}
	router := newDownloaderUploadTestRouter(service, staging)

	uploadResponse := httptest.NewRecorder()
	router.ServeHTTP(uploadResponse, newDownloaderMultipartRequest(t, "installer.iso", "image"))
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var receipt internal.APIResponse[DownloaderUploadReceipt]
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&utilitiesModels.Upload{}).
		Where("id = ?", receipt.Data.UploadID).
		Update("status", utilitiesModels.UploadStatusCompleted).Error; err != nil {
		t.Fatal(err)
	}

	abortRequest := httptest.NewRequest(
		http.MethodDelete,
		"/uploads/"+receipt.Data.UploadID,
		nil,
	)
	abortRequest.Header.Set("X-Test-User-ID", "7")
	abortResponse := httptest.NewRecorder()
	router.ServeHTTP(abortResponse, abortRequest)

	if abortResponse.Code != http.StatusOK {
		t.Fatalf("abort status=%d body=%s", abortResponse.Code, abortResponse.Body.String())
	}
	var payload internal.APIResponse[DownloaderUploadAbortResponse]
	if err := json.Unmarshal(abortResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Message != "downloader_upload_already_completed" ||
		payload.Data.Status != string(utilitiesModels.UploadStatusCompleted) {
		t.Fatalf("unexpected completed abort response: %+v", payload)
	}
	if _, err := os.Stat(filepath.Join(staging, utilities.DownloaderUploadFinalName(receipt.Data.UploadID))); err != nil {
		t.Fatalf("completed source was removed: %v", err)
	}
}

func TestDownloaderUploadFailureDoesNotExposeServerPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)
	staging := filepath.Join(root, "missing", "uploads")
	database := testutil.NewSQLiteTestDB(
		t,
		&utilitiesModels.Upload{},
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	service := &utilities.Service{DB: database}
	router := newDownloaderUploadTestRouter(service, staging)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, newDownloaderMultipartRequest(t, "installer.iso", "image"))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload internal.APIResponse[downloaderUploadErrorData]
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error != "staging_unavailable" || strings.Contains(response.Body.String(), root) {
		t.Fatalf("failure exposed internal detail: %+v", payload)
	}
}

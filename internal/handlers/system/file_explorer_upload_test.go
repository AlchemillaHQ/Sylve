// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package systemHandlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	systemService "github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	uploadCore "github.com/alchemillahq/sylve/internal/upload"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/semaphore"
)

func newFileExplorerSystemService(t *testing.T) *systemService.Service {
	t.Helper()
	return &systemService.Service{
		DB: testutil.NewSQLiteTestDB(t, &utilitiesModels.Upload{}),
	}
}

func newFileExplorerUploadRouter(service *systemService.Service) *gin.Engine {
	return newFileExplorerUploadRouterWithPolicy(service, configuredFileExplorerUploadPolicy())
}

func newFileExplorerUploadRouterWithPolicy(
	service *systemService.Service,
	policy fileExplorerUploadPolicy,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		userID, _ := strconv.ParseUint(c.GetHeader("X-Test-User-ID"), 10, 64)
		c.Set("UserID", uint(userID))
		c.Next()
	})
	router.POST("/upload", newFileExplorerUploadHandler(service, policy, semaphore.NewWeighted(1)))
	router.DELETE("/upload", DeleteUpload(service))
	return router
}

func newMultipartUploadRequest(t *testing.T, destination, filename string, content []byte) *http.Request {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("filepond", filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/upload?path="+url.QueryEscape(destination),
		&body,
	)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-Test-User-ID", "7")
	return request
}

func decodeUploadResponse(t *testing.T, response *httptest.ResponseRecorder) internal.APIResponse[UploadFileResponse] {
	t.Helper()

	var payload internal.APIResponse[UploadFileResponse]
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode upload response: %v; body=%s", err, response.Body.String())
	}
	return payload
}

func TestUploadFileUsesServerIssuedIdentityForRevert(t *testing.T) {
	destination := t.TempDir()
	service := newFileExplorerSystemService(t)
	router := newFileExplorerUploadRouter(service)

	uploadResponse := httptest.NewRecorder()
	router.ServeHTTP(
		uploadResponse,
		newMultipartUploadRequest(t, destination, "installer.iso", []byte("image-data")),
	)
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", uploadResponse.Code, uploadResponse.Body.String())
	}

	payload := decodeUploadResponse(t, uploadResponse)
	if payload.Data.UploadID == "" {
		t.Fatal("expected server-issued upload ID")
	}
	if payload.Data.Bytes != int64(len("image-data")) {
		t.Fatalf("accepted bytes=%d want=%d", payload.Data.Bytes, len("image-data"))
	}
	wantPath := filepath.Join(destination, "installer.iso")
	if payload.Data.Path != wantPath {
		t.Fatalf("uploaded path=%q want=%q", payload.Data.Path, wantPath)
	}
	if content, err := os.ReadFile(wantPath); err != nil || string(content) != "image-data" {
		t.Fatalf("uploaded content=%q err=%v", content, err)
	}
	if info, err := os.Stat(wantPath); err != nil {
		t.Fatalf("stat uploaded file: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("uploaded mode=%#o want=0600", info.Mode().Perm())
	}

	deleteResponse := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/upload", bytes.NewReader(uploadResponse.Body.Bytes()))
	deleteRequest.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	deleteRequest.Header.Set("X-Test-User-ID", "7")
	router.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if _, err := os.Stat(wantPath); !os.IsNotExist(err) {
		t.Fatalf("uploaded file still exists after revert: %v", err)
	}
}

func TestDeleteUploadRejectsCallerProvidedPath(t *testing.T) {
	destination := t.TempDir()
	target := filepath.Join(destination, "keep.iso")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	service := newFileExplorerSystemService(t)
	router := newFileExplorerUploadRouter(service)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodDelete,
		"/upload",
		bytes.NewBufferString(fmt.Sprintf(`{"data":{"path":%q}}`, target)),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Test-User-ID", "7")
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("caller-provided path was removed: %v", err)
	}
}

func TestUploadFileValidatesDestinationBeforeMultipartParsing(t *testing.T) {
	service := newFileExplorerSystemService(t)
	router := newFileExplorerUploadRouter(service)

	notDirectory := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDirectory, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name        string
		destination string
	}{
		{name: "relative", destination: "relative/path"},
		{name: "missing", destination: filepath.Join(t.TempDir(), "missing")},
		{name: "not directory", destination: notDirectory},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/upload?path="+url.QueryEscape(test.destination),
				bytes.NewBufferString("not multipart"),
			)
			request.Header.Set("X-Test-User-ID", "7")
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if !bytes.Contains(response.Body.Bytes(), []byte(`"message":"invalid_destination"`)) {
				t.Fatalf("body=%s", response.Body.String())
			}
		})
	}
}

func TestUploadFileDoesNotDecodeDestinationTwice(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "%2F")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}

	service := newFileExplorerSystemService(t)
	router := newFileExplorerUploadRouter(service)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, newMultipartUploadRequest(t, destination, "disk.img", []byte("disk")))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(destination, "disk.img")); err != nil {
		t.Fatalf("file was not written to literal percent directory: %v", err)
	}
}

func TestUploadFileAcceptsReencodedAbsoluteDestination(t *testing.T) {
	destination := t.TempDir()
	service := newFileExplorerSystemService(t)
	router := newFileExplorerUploadRouter(service)
	response := httptest.NewRecorder()
	request := newMultipartUploadRequest(t, destination, "disk.img", []byte("disk"))
	request.URL.RawQuery = "path=" + url.QueryEscape(url.QueryEscape(destination))

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(destination, "disk.img")); err != nil {
		t.Fatalf("file was not written to re-encoded destination: %v", err)
	}
}

func TestUploadFilePreservesSymlinkedDirectoryNavigation(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "real")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(destination, alias); err != nil {
		t.Fatal(err)
	}

	service := newFileExplorerSystemService(t)
	router := newFileExplorerUploadRouter(service)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, newMultipartUploadRequest(t, alias, "rootfs.txz", []byte("archive")))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	payload := decodeUploadResponse(t, response)
	wantPath := filepath.Join(destination, "rootfs.txz")
	if payload.Data.Path != wantPath {
		t.Fatalf("uploaded path=%q want resolved path=%q", payload.Data.Path, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("file was not uploaded through symlinked directory: %v", err)
	}
}

func TestUploadFileRejectsInvalidFilename(t *testing.T) {
	service := newFileExplorerSystemService(t)
	router := newFileExplorerUploadRouter(service)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, newMultipartUploadRequest(t, t.TempDir(), "..", []byte("invalid")))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"message":"invalid_filename"`)) {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func TestUploadFileRejectsMultipleFilePartsAndCleansPartial(t *testing.T) {
	destination := t.TempDir()
	service := newFileExplorerSystemService(t)
	router := newFileExplorerUploadRouter(service)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, file := range []struct {
		name    string
		content string
	}{
		{name: "first.iso", content: "first"},
		{name: "second.iso", content: "second"},
	} {
		part, err := writer.CreateFormFile(fileExplorerUploadField, file.name)
		if err != nil {
			t.Fatalf("create %s part: %v", file.name, err)
		}
		if _, err := io.WriteString(part, file.content); err != nil {
			t.Fatalf("write %s part: %v", file.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/upload?path="+url.QueryEscape(destination),
		&body,
	)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-Test-User-ID", "7")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assertUploadFailure(t, response, http.StatusBadRequest, "too_many_files")
	assertUploadDirectoryEmpty(t, destination)
}

func TestUploadFileEnforcesFileLimitAndCleansPartial(t *testing.T) {
	destination := t.TempDir()
	service := newFileExplorerSystemService(t)
	router := newFileExplorerUploadRouterWithPolicy(service, fileExplorerUploadPolicy{
		maxFileBytes:    4,
		maxRequestBytes: 4096,
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		newMultipartUploadRequest(t, destination, "disk.img", []byte("12345")),
	)

	failure := assertUploadFailure(t, response, http.StatusRequestEntityTooLarge, "file_too_large")
	if failure.Data.LimitBytes != 4 {
		t.Fatalf("limit bytes=%d want=4", failure.Data.LimitBytes)
	}
	assertUploadDirectoryEmpty(t, destination)
}

func TestUploadFileEnforcesRequestLimitForChunkedBody(t *testing.T) {
	destination := t.TempDir()
	service := newFileExplorerSystemService(t)
	router := newFileExplorerUploadRouterWithPolicy(service, fileExplorerUploadPolicy{
		maxFileBytes:    4096,
		maxRequestBytes: 128,
	})

	request := newMultipartUploadRequest(t, destination, "disk.img", bytes.Repeat([]byte("x"), 512))
	request.ContentLength = -1
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	failure := assertUploadFailure(t, response, http.StatusRequestEntityTooLarge, "request_too_large")
	if failure.Data.LimitBytes != 128 {
		t.Fatalf("limit bytes=%d want=128", failure.Data.LimitBytes)
	}
	assertUploadDirectoryEmpty(t, destination)
}

func TestUploadFileRejectsMalformedMultipartAndCleansPartial(t *testing.T) {
	destination := t.TempDir()
	service := newFileExplorerSystemService(t)
	router := newFileExplorerUploadRouter(service)

	body := strings.Join([]string{
		"--broken",
		`Content-Disposition: form-data; name="filepond"; filename="disk.img"`,
		"Content-Type: application/octet-stream",
		"",
		"partial payload without a closing boundary",
	}, "\r\n")
	request := httptest.NewRequest(
		http.MethodPost,
		"/upload?path="+url.QueryEscape(destination),
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "multipart/form-data; boundary=broken")
	request.Header.Set("X-Test-User-ID", "7")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assertUploadFailure(t, response, http.StatusBadRequest, "malformed_multipart")
	assertUploadDirectoryEmpty(t, destination)
}

func TestUploadFileDoesNotOverwriteExistingDestination(t *testing.T) {
	destination := t.TempDir()
	finalPath := filepath.Join(destination, "disk.img")
	if err := os.WriteFile(finalPath, []byte("existing"), 0o640); err != nil {
		t.Fatal(err)
	}

	service := newFileExplorerSystemService(t)
	router := newFileExplorerUploadRouter(service)
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		newMultipartUploadRequest(t, destination, "disk.img", []byte("replacement")),
	)

	assertUploadFailure(t, response, http.StatusConflict, "file_exists")
	content, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "existing" {
		t.Fatalf("existing content changed to %q", content)
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "disk.img" {
		t.Fatalf("unexpected destination entries: %v", entryNames(entries))
	}
}

func TestUploadFileReturnsRetryableCapacityErrorWithoutReadingQueuedBody(t *testing.T) {
	destination := t.TempDir()
	service := newFileExplorerSystemService(t)
	router := newFileExplorerUploadRouterWithPolicy(service, fileExplorerUploadPolicy{
		maxFileBytes:    4096,
		maxRequestBytes: 4096,
	})

	blockedBody := &blockingUploadBody{
		readStarted: make(chan struct{}, 1),
		release:     make(chan struct{}),
	}
	firstRequest := httptest.NewRequest(
		http.MethodPost,
		"/upload?path="+url.QueryEscape(destination),
		blockedBody,
	)
	firstRequest.Header.Set("Content-Type", "multipart/form-data; boundary=blocked")
	firstRequest.Header.Set("X-Test-User-ID", "7")
	firstResponse := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		router.ServeHTTP(firstResponse, firstRequest)
		close(firstDone)
	}()

	select {
	case <-blockedBody.readStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not begin reading")
	}

	secondBody := &countingReadCloser{reader: strings.NewReader("must not be read")}
	secondRequest := httptest.NewRequest(
		http.MethodPost,
		"/upload?path="+url.QueryEscape(destination),
		secondBody,
	)
	secondRequest.Header.Set("Content-Type", "multipart/form-data; boundary=second")
	secondRequest.Header.Set("X-Test-User-ID", "7")
	secondResponse := httptest.NewRecorder()
	router.ServeHTTP(secondResponse, secondRequest)

	failure := assertUploadFailure(
		t,
		secondResponse,
		http.StatusTooManyRequests,
		"upload_capacity_exhausted",
	)
	if !failure.Data.Retryable {
		t.Fatal("capacity response must be retryable")
	}
	if got := secondResponse.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After=%q want=1", got)
	}
	if secondBody.reads != 0 {
		t.Fatalf("queued request body was read %d times", secondBody.reads)
	}

	close(blockedBody.release)
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not stop")
	}
}

func TestUploadFileCancellationRemovesDestinationPartial(t *testing.T) {
	destination := t.TempDir()
	service := newFileExplorerSystemService(t)
	router := newFileExplorerUploadRouterWithPolicy(service, fileExplorerUploadPolicy{
		maxFileBytes:    4096,
		maxRequestBytes: 8192,
	})

	pipeReader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)
	request := httptest.NewRequest(
		http.MethodPost,
		"/upload?path="+url.QueryEscape(destination),
		pipeReader,
	)
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-Test-User-ID", "7")

	prefixWritten := make(chan struct{})
	releaseWriter := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		part, err := writer.CreateFormFile(fileExplorerUploadField, "cancelled.img")
		if err != nil {
			_ = pipeWriter.CloseWithError(err)
			return
		}
		if _, err := io.WriteString(part, "prefix"); err != nil {
			_ = pipeWriter.CloseWithError(err)
			return
		}
		close(prefixWritten)
		<-releaseWriter
		_ = pipeWriter.CloseWithError(context.Canceled)
	}()

	response := httptest.NewRecorder()
	requestDone := make(chan struct{})
	go func() {
		router.ServeHTTP(response, request)
		close(requestDone)
	}()

	select {
	case <-prefixWritten:
	case <-time.After(2 * time.Second):
		t.Fatal("upload did not begin")
	}

	partialPath := waitForUploadPartial(t, destination)
	if strings.Contains(filepath.Base(partialPath), "cancelled.img") {
		t.Fatalf("partial path contains client filename: %s", partialPath)
	}
	info, err := os.Lstat(partialPath)
	if err != nil {
		t.Fatalf("stat partial: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("partial mode=%v want regular 0600", info.Mode())
	}
	if _, err := os.Lstat(filepath.Join(destination, "cancelled.img")); !os.IsNotExist(err) {
		t.Fatalf("final name became visible before publication: %v", err)
	}

	cancel()
	close(releaseWriter)
	select {
	case <-requestDone:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled upload did not stop")
	}
	<-writerDone

	assertUploadFailure(t, response, http.StatusRequestTimeout, "upload_cancelled")
	assertUploadDirectoryEmpty(t, destination)
}

func TestPublishFileExplorerUploadIsAtomicAndDoesNotOverwrite(t *testing.T) {
	destination := t.TempDir()
	first := filepath.Join(destination, ".first.partial")
	second := filepath.Join(destination, ".second.partial")
	finalPath := filepath.Join(destination, "disk.img")
	if err := os.WriteFile(first, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for _, partial := range []string{first, second} {
		waitGroup.Add(1)
		go func(path string) {
			defer waitGroup.Done()
			<-start
			results <- uploadCore.PublishNoReplace(path, finalPath)
		}(partial)
	}
	close(start)
	waitGroup.Wait()
	close(results)

	successes := 0
	collisions := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, os.ErrExist):
			collisions++
		default:
			t.Fatalf("unexpected publish error: %v", err)
		}
	}
	if successes != 1 || collisions != 1 {
		t.Fatalf("successes=%d collisions=%d", successes, collisions)
	}
	content, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "first" && string(content) != "second" {
		t.Fatalf("unexpected published content %q", content)
	}
}

func TestFileExplorerUploadFilesystemFailuresAreStructured(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		statusCode int
		code       string
	}{
		{
			name:       "no space",
			err:        &os.PathError{Op: "write", Path: "/upload", Err: syscall.ENOSPC},
			statusCode: http.StatusInsufficientStorage,
			code:       "insufficient_storage",
		},
		{
			name:       "quota",
			err:        &os.PathError{Op: "write", Path: "/upload", Err: syscall.EDQUOT},
			statusCode: http.StatusInsufficientStorage,
			code:       "insufficient_storage",
		},
		{
			name:       "permission",
			err:        &os.PathError{Op: "open", Path: "/upload", Err: syscall.EACCES},
			statusCode: http.StatusForbidden,
			code:       "upload_permission_denied",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure := uploadCore.FilesystemFailure(test.err, "upload_write_failed")
			if failure.StatusCode != test.statusCode || failure.Code != test.code {
				t.Fatalf(
					"failure=(%d, %q), want=(%d, %q)",
					failure.StatusCode,
					failure.Code,
					test.statusCode,
					test.code,
				)
			}
		})
	}
}

type uploadFailureResponse = internal.APIResponse[fileExplorerUploadErrorData]

func assertUploadFailure(
	t *testing.T,
	response *httptest.ResponseRecorder,
	statusCode int,
	code string,
) uploadFailureResponse {
	t.Helper()
	if response.Code != statusCode {
		t.Fatalf("status=%d want=%d body=%s", response.Code, statusCode, response.Body.String())
	}

	var payload uploadFailureResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode failure response: %v; body=%s", err, response.Body.String())
	}
	if payload.Message != code || payload.Data.Code != code {
		t.Fatalf("failure code=(%q, %q) want=%q", payload.Message, payload.Data.Code, code)
	}
	return payload
}

func assertUploadDirectoryEmpty(t *testing.T, destination string) {
	t.Helper()
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("destination contains partial/final files: %v", entryNames(entries))
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func waitForUploadPartial(t *testing.T, destination string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(destination)
		if err != nil {
			t.Fatalf("read destination: %v", err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".sylve-upload-") &&
				strings.HasSuffix(entry.Name(), ".partial") {
				return filepath.Join(destination, entry.Name())
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("partial upload did not appear in destination")
	return ""
}

type blockingUploadBody struct {
	readStarted chan struct{}
	release     chan struct{}
}

func (b *blockingUploadBody) Read(_ []byte) (int, error) {
	select {
	case b.readStarted <- struct{}{}:
	default:
	}
	<-b.release
	return 0, io.EOF
}

func (b *blockingUploadBody) Close() error {
	return nil
}

type countingReadCloser struct {
	reader io.Reader
	reads  int
}

func (r *countingReadCloser) Read(buffer []byte) (int, error) {
	r.reads++
	return r.reader.Read(buffer)
}

func (r *countingReadCloser) Close() error {
	return nil
}

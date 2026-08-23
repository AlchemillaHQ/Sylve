// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilitiesHandlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func newSignedDownloadHandlerService(t *testing.T) (*utilities.Service, utilitiesModels.Downloads) {
	t.Helper()
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	if err := os.MkdirAll(config.GetDownloadsPath("http"), 0o755); err != nil {
		t.Fatal(err)
	}

	database := testutil.NewSQLiteTestDB(
		t,
		&models.SystemSecrets{},
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	filePath := filepath.Join(config.GetDownloadsPath("http"), "installer.iso")
	if err := os.WriteFile(filePath, []byte("download-payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	download := utilitiesModels.Downloads{
		UUID:     utils.GenerateRandomUUID(),
		Path:     filePath,
		Name:     "installer.iso",
		Type:     utilitiesModels.DownloadTypeHTTP,
		URL:      "https://example.invalid/installer.iso",
		Progress: 100,
		Size:     16,
		Status:   utilitiesModels.DownloadStatusDone,
	}
	if err := database.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	return &utilities.Service{DB: database}, download
}

func TestSignedDownloadHandlerSupportsRangesAndSafeHeaders(t *testing.T) {
	service, download := newSignedDownloadHandlerService(t)
	node, err := utils.GetSystemHostname()
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour).Unix()
	signature, err := service.BuildSignedDownloadSignature(node, download.UUID, int(download.ID), expires)
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{
		"expires": []string{strconv.FormatInt(expires, 10)},
		"id":      []string{strconv.Itoa(int(download.ID))},
		"node":    []string{node},
		"sig":     []string{signature},
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/utilities/downloads/:uuid", DownloadFileFromSignedURL(service))
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/utilities/downloads/"+download.UUID+"?"+query.Encode(),
		nil,
	)
	request.Header.Set("Range", "bytes=0-7")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusPartialContent || response.Body.String() != "download" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control=%q", got)
	}
	if got := response.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy=%q", got)
	}
	if got := response.Header().Get("Content-Disposition"); !strings.Contains(got, "installer.iso") {
		t.Fatalf("Content-Disposition=%q", got)
	}
}

func TestSignedDownloadHandlerRejectsTamperedSignatureWithoutInternalDetails(t *testing.T) {
	service, download := newSignedDownloadHandlerService(t)
	node, err := utils.GetSystemHostname()
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{
		"expires": []string{strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)},
		"id":      []string{strconv.Itoa(int(download.ID))},
		"node":    []string{node},
		"sig":     []string{"tampered"},
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/utilities/downloads/:uuid", DownloadFileFromSignedURL(service))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(
		http.MethodGet,
		"/api/utilities/downloads/"+download.UUID+"?"+query.Encode(),
		nil,
	))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["message"] != "invalid_or_expired_signature" || strings.Contains(response.Body.String(), download.Path) {
		t.Fatalf("unexpected public error: %s", response.Body.String())
	}
}

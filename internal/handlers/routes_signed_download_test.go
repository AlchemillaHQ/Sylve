// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
)

func TestSignedDownloadRoutesServeHead(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	if err := os.MkdirAll(config.GetDownloadsPath("http"), 0o755); err != nil {
		t.Fatal(err)
	}

	database := testutil.NewSQLiteTestDB(t,
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

	service := &utilities.Service{DB: database}
	result, err := service.CreateSignedDownloadURL(download.UUID, download.Name)
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerSignedDownloadRoutes(router.Group("/api"), database, service)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodHead, result.URL, nil))

	if response.Code != http.StatusOK {
		t.Fatalf("HEAD status=%d body=%q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Disposition"); !strings.Contains(got, download.Name) {
		t.Fatalf("HEAD Content-Disposition=%q", got)
	}
	if response.Body.Len() != 0 {
		t.Fatalf("HEAD body=%q", response.Body.String())
	}
}

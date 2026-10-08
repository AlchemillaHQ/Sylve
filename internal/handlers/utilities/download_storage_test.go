// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilitiesHandlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/downloadstorage"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestDownloadStorageDiscoveryDoesNotProvisionAndKeepsDefault(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)
	database := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	if err := database.Create(&models.BasicSettings{Pools: []string{"missing"}}).Error; err != nil {
		t.Fatal(err)
	}
	service := utilities.NewUtilitiesService(database, nil, nil, nil).(*utilities.Service)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/storage", GetDownloadStorage(service))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/storage", nil))
	var payload internal.APIResponse[downloadstorage.Choices]
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(payload.Data.Choices) != 2 || !payload.Data.Choices[0].Available || payload.Data.Choices[1].Available || payload.Data.Choices[1].Reason != downloadstorage.ErrUnavailable.Error() {
		t.Fatalf("choices = %+v, HTTP %d", payload, response.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "downloads")); !os.IsNotExist(err) {
		t.Fatalf("GET created payload storage: %v", err)
	}
}

func TestDownloaderUploadRejectsInvalidStorageBeforeReceiving(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)
	service := utilities.NewUtilitiesService(nil, nil, nil, nil).(*utilities.Service)
	router := newDownloaderUploadTestRouter(service, "")
	for _, query := range []string{"storagePool=tank&storagePool=other", "storagePool=%2Farbitrary%2Fpath"} {
		request := newDownloaderMultipartRequest(t, "installer.iso", "payload")
		request.URL.RawQuery = query
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), downloadstorage.ErrInvalid.Error()) {
			t.Fatalf("invalid target admitted: HTTP %d %s", response.Code, response.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "downloads")); !os.IsNotExist(err) {
		t.Fatalf("invalid target used Default staging: %v", err)
	}
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilitiesHandlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/gin-gonic/gin"
)

func TestDownloadFileAPIRejectsTarAndRawCombination(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	database := testutil.NewSQLiteTestDB(
		t,
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	service := &utilities.Service{DB: database}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/downloads", DownloadFile(service))

	request := httptest.NewRequest(
		http.MethodPost,
		"/downloads",
		bytes.NewBufferString(`{
			"url":"https://example.test/base.txz",
			"downloadType":"base-rootfs",
			"automaticExtraction":true,
			"automaticRawConversion":true
		}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assertIncompatiblePostProcessResponse(t, response)
}

func TestUpdateDownloadAPIRejectsTarAndRawCombination(t *testing.T) {
	database := testutil.NewSQLiteTestDB(
		t,
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	download := utilitiesModels.Downloads{
		UUID:                utils.GenerateRandomUUID(),
		Path:                filepath.Join(t.TempDir(), "base.txz"),
		Name:                "base.txz",
		Type:                utilitiesModels.DownloadTypePath,
		URL:                 filepath.Join(t.TempDir(), "source.txz"),
		Progress:            100,
		Status:              utilitiesModels.DownloadStatusDone,
		AutomaticExtraction: true,
	}
	if err := database.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	service := &utilities.Service{DB: database}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PATCH("/downloads/:id", UpdateDownload(service))

	request := httptest.NewRequest(
		http.MethodPatch,
		"/downloads/"+strconv.FormatUint(uint64(download.ID), 10),
		bytes.NewBufferString(`{"automaticRawConversion":true}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assertIncompatiblePostProcessResponse(t, response)
}

func TestUpdateDownloadAPIRejectsNonPositiveID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PATCH("/downloads/:id", UpdateDownload(&utilities.Service{}))

	request := httptest.NewRequest(
		http.MethodPatch,
		"/downloads/0",
		bytes.NewBufferString(`{"name":"display.img"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func assertIncompatiblePostProcessResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
) {
	t.Helper()
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	var payload internal.APIResponse[any]
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Message != "incompatible_post_processing_options" ||
		payload.Error != "incompatible_post_processing_options" {
		t.Fatalf("unexpected API response: %+v", payload)
	}
}

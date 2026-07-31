// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilities

import (
	"strings"
	"testing"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestDownloadFileRejectsNonRegularExistingPath(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	service := &Service{DB: db}

	_, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{
		URL:          t.TempDir(),
		DownloadType: utilitiesModels.DownloadUTypeOther,
	})
	if err == nil || !strings.Contains(err.Error(), "source_not_regular_file") {
		t.Fatalf("error=%v want source_not_regular_file", err)
	}
}

func TestDownloadFileReportsMissingExistingPath(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	service := &Service{DB: db}

	_, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{
		URL:          t.TempDir() + "/missing.img",
		DownloadType: utilitiesModels.DownloadUTypeOther,
	})
	if err == nil || !strings.Contains(err.Error(), "file_not_found") {
		t.Fatalf("error=%v want file_not_found", err)
	}
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func newSigningTestService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	for _, kind := range []string{"http", "path", "torrents", "extracted"} {
		if err := os.MkdirAll(config.GetDownloadsPath(kind), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	db := testutil.NewSQLiteTestDB(
		t,
		&models.SystemSecrets{},
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	return &Service{
		DB: db,
		uploadHostnameFn: func() (string, error) {
			return "test-node", nil
		},
	}
}

func TestBuildAndValidateDownloadSignature(t *testing.T) {
	svc := newSigningTestService(t)

	input := "file-uuid:42"
	expires := int64(1893456000)

	sig, err := svc.BuildDownloadSignature(input, expires)
	if err != nil {
		t.Fatalf("build_signature_failed: %v", err)
	}
	if sig == "" {
		t.Fatal("expected_non_empty_signature")
	}

	valid, err := svc.ValidateDownloadSignature(input, expires, sig)
	if err != nil {
		t.Fatalf("validate_signature_failed: %v", err)
	}
	if !valid {
		t.Fatal("expected_signature_to_be_valid")
	}

	valid, err = svc.ValidateDownloadSignature(input, expires, sig+"x")
	if err != nil {
		t.Fatalf("validate_tampered_signature_failed: %v", err)
	}
	if valid {
		t.Fatal("expected_tampered_signature_to_be_invalid")
	}
}

func TestDownloadSigningSecretCreatedOnceAndReused(t *testing.T) {
	svc := newSigningTestService(t)

	first, err := svc.getOrCreateDownloadSigningSecret()
	if err != nil {
		t.Fatalf("first_secret_create_failed: %v", err)
	}
	if first == "" {
		t.Fatal("expected_non_empty_first_secret")
	}

	second, err := svc.getOrCreateDownloadSigningSecret()
	if err != nil {
		t.Fatalf("second_secret_load_failed: %v", err)
	}
	if second == "" {
		t.Fatal("expected_non_empty_second_secret")
	}
	if first != second {
		t.Fatal("expected_same_secret_to_be_reused")
	}

	var count int64
	if err := svc.DB.Model(&models.SystemSecrets{}).
		Where("name = ?", downloadSigningSecretName).
		Count(&count).Error; err != nil {
		t.Fatalf("failed_to_count_download_signing_secret: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected_single_download_signing_secret_row_got: %d", count)
	}
}

func TestDownloadSigningSecretConcurrentInitializationReusesOneValue(t *testing.T) {
	svc := newSigningTestService(t)

	const callers = 8
	values := make(chan string, callers)
	errorsFound := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := svc.getOrCreateDownloadSigningSecret()
			if err != nil {
				errorsFound <- err
				return
			}
			values <- value
		}()
	}
	wg.Wait()
	close(values)
	close(errorsFound)

	for err := range errorsFound {
		t.Fatalf("concurrent secret initialization: %v", err)
	}
	var expected string
	for value := range values {
		if expected == "" {
			expected = value
		}
		if value == "" || value != expected {
			t.Fatalf("concurrent secrets differ: got=%q expected=%q", value, expected)
		}
	}
}

func TestCreateSignedDownloadURLBindsNodeAndCanonicalTarget(t *testing.T) {
	svc := newSigningTestService(t)
	downloadUUID := utils.GenerateRandomUUID()
	filePath := filepath.Join(config.GetDownloadsPath("http"), "installer.iso")
	if err := os.WriteFile(filePath, []byte("installer"), 0o600); err != nil {
		t.Fatal(err)
	}
	download := utilitiesModels.Downloads{
		UUID:     downloadUUID,
		Path:     filePath,
		Name:     "installer.iso",
		Type:     utilitiesModels.DownloadTypeHTTP,
		URL:      "https://example.invalid/installer.iso",
		Progress: 100,
		Size:     9,
		Status:   utilitiesModels.DownloadStatusDone,
	}
	if err := svc.DB.Create(&download).Error; err != nil {
		t.Fatal(err)
	}

	result, err := svc.CreateSignedDownloadURL(download.UUID, download.Name)
	if err != nil {
		t.Fatalf("create signed URL: %v", err)
	}
	if result.ExpiresAt.Before(time.Now().Add(SignedDownloadURLValidity - time.Minute)) {
		t.Fatalf("unexpected expiry: %s", result.ExpiresAt)
	}
	parsed, err := url.Parse(result.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Query().Get("node"); got != "test-node" {
		t.Fatalf("node=%q", got)
	}
	resourceID, err := strconv.Atoi(parsed.Query().Get("id"))
	if err != nil {
		t.Fatal(err)
	}
	expires, err := strconv.ParseInt(parsed.Query().Get("expires"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := svc.ValidateSignedDownloadSignature(
		"test-node",
		download.UUID,
		resourceID,
		expires,
		parsed.Query().Get("sig"),
	)
	if err != nil || !valid {
		t.Fatalf("validate signed URL: valid=%v err=%v", valid, err)
	}
	valid, err = svc.ValidateSignedDownloadSignature(
		"different-node",
		download.UUID,
		resourceID,
		expires,
		parsed.Query().Get("sig"),
	)
	if err != nil || valid {
		t.Fatalf("tampered node validation: valid=%v err=%v", valid, err)
	}
}

func TestResolveSignedTorrentTargetRejectsEscapingCatalogPath(t *testing.T) {
	svc := newSigningTestService(t)
	downloadUUID := utils.GenerateRandomUUID()
	downloadRoot := filepath.Join(config.GetDownloadsPath("torrents"), downloadUUID)
	if err := os.MkdirAll(downloadRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	download := utilitiesModels.Downloads{
		UUID:     downloadUUID,
		Path:     downloadRoot,
		Name:     "unsafe torrent",
		Type:     utilitiesModels.DownloadTypeTorrent,
		URL:      "magnet:?xt=urn:btih:unsafe",
		Progress: 100,
		Status:   utilitiesModels.DownloadStatusDone,
	}
	if err := svc.DB.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	file := utilitiesModels.DownloadedFile{
		DownloadID: int(download.ID),
		Name:       "../outside.iso",
		Size:       1,
	}
	if err := svc.DB.Create(&file).Error; err != nil {
		t.Fatal(err)
	}

	_, err := svc.ResolveSignedDownloadTargetByID(download.UUID, file.ID)
	if !errors.Is(err, ErrSignedDownloadUnsafe) {
		t.Fatalf("error=%v want ErrSignedDownloadUnsafe", err)
	}
}

func TestResolveSignedDownloadRequiresCompletedRegularFile(t *testing.T) {
	svc := newSigningTestService(t)
	downloadUUID := utils.GenerateRandomUUID()
	filePath := filepath.Join(config.GetDownloadsPath("path"), "pending.img")
	if err := os.WriteFile(filePath, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	download := utilitiesModels.Downloads{
		UUID:     downloadUUID,
		Path:     filePath,
		Name:     "pending.img",
		Type:     utilitiesModels.DownloadTypePath,
		URL:      "/source/pending.img",
		Progress: 50,
		Status:   utilitiesModels.DownloadStatusProcessing,
	}
	if err := svc.DB.Create(&download).Error; err != nil {
		t.Fatal(err)
	}

	_, err := svc.ResolveSignedDownloadTargetByName(download.UUID, download.Name)
	if !errors.Is(err, ErrSignedDownloadNotReady) {
		t.Fatalf("error=%v want ErrSignedDownloadNotReady", err)
	}
}

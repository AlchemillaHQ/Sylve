// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newSigningTestService(t *testing.T) *Service {
	t.Helper()

	db := testutil.NewSQLiteTestDB(t, &models.SystemSecrets{})
	return &Service{DB: db}
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

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import (
	"net/http"
	"testing"
)

func TestIsPublicSignedDownloadRequest(t *testing.T) {
	validUUIDPath := "/api/utilities/downloads/550e8400-e29b-41d4-a716-446655440000"

	if !isPublicSignedDownloadRequest(http.MethodGet, validUUIDPath) {
		t.Fatal("expected_valid_public_download_path_to_pass")
	}

	if isPublicSignedDownloadRequest(http.MethodDelete, validUUIDPath) {
		t.Fatal("expected_non_get_method_to_fail")
	}

	if isPublicSignedDownloadRequest(http.MethodGet, "/api/utilities/downloads/123") {
		t.Fatal("expected_non_uuid_path_to_fail")
	}

	if isPublicSignedDownloadRequest(http.MethodGet, validUUIDPath+"/extra") {
		t.Fatal("expected_nested_path_to_fail")
	}

	if isPublicSignedDownloadRequest(http.MethodGet, "/api/utilities/downloads/signed-url") {
		t.Fatal("expected_signed_url_endpoint_to_require_auth")
	}
}

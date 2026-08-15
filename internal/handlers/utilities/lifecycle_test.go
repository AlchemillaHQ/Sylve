// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilitiesHandlers

import (
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal/services/utilities"
)

func TestDownloadMutationErrorMapsInactiveUtilitiesToServiceUnavailable(t *testing.T) {
	status, message := downloadMutationError(utilities.ErrUtilitiesNotReady, "fallback")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", status, http.StatusServiceUnavailable)
	}
	if message != "utilities_not_ready" {
		t.Fatalf("message = %q, want utilities_not_ready", message)
	}
}

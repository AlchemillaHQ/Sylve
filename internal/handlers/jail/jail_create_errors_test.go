// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"fmt"
	"net/http"
	"testing"
)

func TestClassifyCreateJailError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "stale artifacts are conflict",
			err:        fmt.Errorf("jail_create_stale_artifacts_detected: ctid=801 root_dataset_exists=true"),
			wantStatus: http.StatusConflict,
			wantCode:   "jail_create_stale_artifacts_detected",
		},
		{
			name:       "legacy dynamic ctid conflict maps to stable code",
			err:        fmt.Errorf("jail_with_ctid_801_already_exists"),
			wantStatus: http.StatusConflict,
			wantCode:   "jail_with_ctid_already_exists",
		},
		{
			name:       "invalid base path is bad request",
			err:        fmt.Errorf("base_is_not_a_directory"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "base_is_not_a_directory",
		},
		{
			name:       "runtime wrapper returns runtime failure code",
			err:        fmt.Errorf("failed_to_create_jail: duplicated key not allowed"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "jail_create_runtime_failure",
		},
		{
			name:       "tx wrapper returns database failure code",
			err:        fmt.Errorf("failed_to_begin_tx: database unavailable"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "jail_create_database_failure",
		},
		{
			name:       "unknown error falls back to generic code",
			err:        fmt.Errorf("something unexpected happened"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "failed_to_create_jail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotStatus, gotCode := classifyCreateJailError(tt.err)
			if gotStatus != tt.wantStatus {
				t.Fatalf("expected status=%d, got status=%d (code=%s)", tt.wantStatus, gotStatus, gotCode)
			}
			if gotCode != tt.wantCode {
				t.Fatalf("expected code=%q, got code=%q", tt.wantCode, gotCode)
			}
		})
	}
}

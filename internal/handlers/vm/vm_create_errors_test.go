// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"fmt"
	"net/http"
	"testing"
)

func TestClassifyCreateVMError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name: "invalid iso format from resolver details",
			err: fmt.Errorf(
				"failed_to_create_lv_vm: failed to generate VM XML: failed_to_find_iso: iso_or_img_not_found: " +
					"main=/var/db/sylve/downloads/http/ubuntu-25.10-server-cloudimg-amd64 (exists=true, allowed=false) extracted= (exists=false, allowed=false)",
			),
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_iso_or_image_format",
		},
		{
			name:       "stale artifacts are conflict",
			err:        fmt.Errorf("vm_create_stale_artifacts_detected: rid=801 root_dataset_exists=true stale_storage_dataset_rows=2"),
			wantStatus: http.StatusConflict,
			wantCode:   "vm_create_stale_artifacts_detected",
		},
		{
			name:       "rid or name conflict",
			err:        fmt.Errorf("rid_or_name_already_in_use"),
			wantStatus: http.StatusConflict,
			wantCode:   "rid_or_name_already_in_use",
		},
		{
			name:       "libvirt domain already exists maps to vm id exists",
			err:        fmt.Errorf("failed_to_create_lv_vm: failed to define VM domain: domain '801' already exists"),
			wantStatus: http.StatusConflict,
			wantCode:   "vm_id_already_exists",
		},
		{
			name: "iso missing path maps to not found",
			err: fmt.Errorf(
				"failed_to_create_lv_vm: failed to generate VM XML: failed_to_find_iso: iso_or_img_not_found: " +
					"main=/var/db/sylve/downloads/http/missing.iso (exists=false, allowed=false) extracted= (exists=false, allowed=false)",
			),
			wantStatus: http.StatusBadRequest,
			wantCode:   "iso_or_image_not_found",
		},
		{
			name:       "db insert wrapper uses database failure code",
			err:        fmt.Errorf("failed_to_create_vm_with_associations: UNIQUE constraint failed"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "vm_create_database_failure",
		},
		{
			name:       "unknown error falls back to generic code",
			err:        fmt.Errorf("something strange happened"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "failed_to_create_vm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotStatus, gotCode := classifyCreateVMError(tt.err)
			if gotStatus != tt.wantStatus {
				t.Fatalf("expected status=%d, got status=%d (code=%s)", tt.wantStatus, gotStatus, gotCode)
			}
			if gotCode != tt.wantCode {
				t.Fatalf("expected code=%q, got code=%q", tt.wantCode, gotCode)
			}
		})
	}
}

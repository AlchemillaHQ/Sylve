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
			name:       "shared guest ID conflict",
			err:        fmt.Errorf("guest_id_already_in_use: guest_id=801 node_id=node-a guest_type=jail"),
			wantStatus: http.StatusConflict,
			wantCode:   "guest_id_already_in_use",
		},
		{
			name:       "cluster inventory unavailable",
			err:        fmt.Errorf("guest_identity_inventory_unavailable: remote node unavailable"),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "guest_identity_inventory_unavailable",
		},
		{
			name:       "existing inventory conflict",
			err:        fmt.Errorf("guest_identity_inventory_conflict: duplicate ID"),
			wantStatus: http.StatusConflict,
			wantCode:   "guest_identity_inventory_conflict",
		},
		{
			name:       "replication ownership denied",
			err:        fmt.Errorf("replication_lease_not_owned"),
			wantStatus: http.StatusForbidden,
			wantCode:   "replication_lease_not_owned",
		},
		{
			name:       "create dependency unavailable",
			err:        fmt.Errorf("failed_to_list_usable_pools_for_vm_create_precheck: system unavailable"),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "vm_create_dependency_not_ready",
		},
		{
			name:       "UEFI is unavailable on arm64",
			err:        fmt.Errorf("uefi_firmware_not_available_on_arm64"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "uefi_firmware_not_available_on_arm64",
		},
		{
			name:       "U-Boot is unavailable off arm64",
			err:        fmt.Errorf("uboot_only_available_on_arm64"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "uboot_only_available_on_arm64",
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

func TestClassifyUpdateVMDescriptionError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "not found", err: fmt.Errorf("vm_not_found: 100"), wantStatus: http.StatusNotFound, wantCode: "vm_not_found"},
		{name: "invalid description", err: fmt.Errorf("invalid_description"), wantStatus: http.StatusBadRequest, wantCode: "invalid_description"},
		{name: "ownership denied", err: fmt.Errorf("replication_lease_not_owned"), wantStatus: http.StatusForbidden, wantCode: "replication_lease_not_owned"},
		{name: "mountpoint conflict", err: fmt.Errorf("failed_to_write_vm_json: filesystem_dataset_mountpoint_not_usable"), wantStatus: http.StatusConflict, wantCode: "filesystem_dataset_mountpoint_not_usable"},
		{name: "ownership check failed", err: fmt.Errorf("replication_lease_check_failed: database unavailable"), wantStatus: http.StatusInternalServerError, wantCode: "replication_lease_check_failed"},
		{name: "unknown", err: fmt.Errorf("write failed"), wantStatus: http.StatusInternalServerError, wantCode: "failed_to_update_vm_description"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotStatus, gotCode := classifyUpdateVMDescriptionError(tt.err)
			if gotStatus != tt.wantStatus || gotCode != tt.wantCode {
				t.Fatalf("classification = (%d, %q), want (%d, %q)", gotStatus, gotCode, tt.wantStatus, tt.wantCode)
			}
		})
	}
}

func TestClassifyUpdateVMNameError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "not found", err: fmt.Errorf("vm_not_found: 100"), wantStatus: http.StatusNotFound, wantCode: "vm_not_found"},
		{name: "invalid name", err: fmt.Errorf("invalid_vm_name"), wantStatus: http.StatusBadRequest, wantCode: "invalid_vm_name"},
		{name: "duplicate name", err: fmt.Errorf("vm_name_already_in_use"), wantStatus: http.StatusConflict, wantCode: "vm_name_already_in_use"},
		{name: "ownership denied", err: fmt.Errorf("replication_lease_not_owned"), wantStatus: http.StatusForbidden, wantCode: "replication_lease_not_owned"},
		{name: "mountpoint conflict", err: fmt.Errorf("failed_to_write_vm_json: filesystem_dataset_mountpoint_not_usable"), wantStatus: http.StatusConflict, wantCode: "filesystem_dataset_mountpoint_not_usable"},
		{name: "ownership check failed", err: fmt.Errorf("replication_lease_check_failed: database unavailable"), wantStatus: http.StatusInternalServerError, wantCode: "replication_lease_check_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotStatus, gotCode := classifyUpdateVMNameError(tt.err)
			if gotStatus != tt.wantStatus || gotCode != tt.wantCode {
				t.Fatalf("classification = (%d, %q), want (%d, %q)", gotStatus, gotCode, tt.wantStatus, tt.wantCode)
			}
		})
	}
}

func TestClassifyRemoveVMError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "not found", err: fmt.Errorf("vm_not_found: 100"), wantStatus: http.StatusNotFound, wantCode: "vm_not_found"},
		{name: "ownership denied", err: fmt.Errorf("replication_lease_not_owned"), wantStatus: http.StatusForbidden, wantCode: "replication_lease_not_owned"},
		{name: "policy conflict", err: fmt.Errorf("guest_delete_requires_replication_policy_removed"), wantStatus: http.StatusConflict, wantCode: "guest_delete_requires_replication_policy_removed"},
		{name: "topology conflict", err: fmt.Errorf("replication_storage_topology_change_requires_policy_disabled"), wantStatus: http.StatusConflict, wantCode: "replication_storage_topology_change_requires_policy_disabled"},
		{name: "live domain", err: fmt.Errorf("vm_not_orphaned"), wantStatus: http.StatusConflict, wantCode: "vm_not_orphaned"},
		{name: "operation conflict", err: fmt.Errorf("lifecycle_task_in_progress"), wantStatus: http.StatusConflict, wantCode: "vm_operation_in_progress"},
		{name: "orphan check unavailable", err: fmt.Errorf("vm_orphan_check_unavailable: connection refused"), wantStatus: http.StatusServiceUnavailable, wantCode: "vm_delete_dependency_not_ready"},
		{name: "unknown", err: fmt.Errorf("cleanup failed"), wantStatus: http.StatusInternalServerError, wantCode: "fallback_code"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotStatus, gotCode := classifyRemoveVMError(tt.err, "fallback_code")
			if gotStatus != tt.wantStatus || gotCode != tt.wantCode {
				t.Fatalf("classification = (%d, %q), want (%d, %q)", gotStatus, gotCode, tt.wantStatus, tt.wantCode)
			}
		})
	}
}

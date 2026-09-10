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
			name:       "shared guest ID conflict",
			err:        fmt.Errorf("guest_id_already_in_use: guest_id=801 node_id=node-a guest_type=vm"),
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
			name:       "replication lease denial is forbidden",
			err:        fmt.Errorf("replication_lease_not_owned"),
			wantStatus: http.StatusForbidden,
			wantCode:   "replication_lease_not_owned",
		},
		{
			name:       "existing inventory conflict",
			err:        fmt.Errorf("guest_identity_inventory_conflict: duplicate ID"),
			wantStatus: http.StatusConflict,
			wantCode:   "guest_identity_inventory_conflict",
		},
		{
			name:       "invalid base path is bad request",
			err:        fmt.Errorf("base_is_not_a_directory"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "base_is_not_a_directory",
		},
		{
			name:       "bootstrap and download are mutually exclusive",
			err:        fmt.Errorf("base_and_bootstrap_name_are_mutually_exclusive"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "base_and_bootstrap_name_are_mutually_exclusive",
		},
		{
			name:       "missing jail dependency is unavailable",
			err:        fmt.Errorf("failed_to_get_usable_pools: zfs unavailable"),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "jail_create_dependency_not_ready",
		},
		{
			name:       "manual switch inspection is unavailable",
			err:        fmt.Errorf("failed_to_inspect_manual_switch_vlan_state: LAN: ioctl failed"),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "failed_to_inspect_manual_switch_vlan_state",
		},
		{
			name:       "filtered switch runtime drift is conflict",
			err:        fmt.Errorf("failed_to_sync_network: filtered_switch_runtime_mismatch: LAN: bridge VLAN filtering is disabled"),
			wantStatus: http.StatusConflict,
			wantCode:   "filtered_switch_runtime_mismatch",
		},
		{
			name:       "unsupported manual switch qinq is conflict",
			err:        fmt.Errorf("filtered_switch_qinq_unsupported: LAN"),
			wantStatus: http.StatusConflict,
			wantCode:   "filtered_switch_qinq_unsupported",
		},
		{
			name:       "invalid VLAN policy is bad request",
			err:        fmt.Errorf("invalid_vlan_policy: invalid VLAN port mode"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_vlan_policy",
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

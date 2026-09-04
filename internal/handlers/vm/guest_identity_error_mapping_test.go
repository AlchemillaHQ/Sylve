// SPDX-License-Identifier: BSD-2-Clause

package libvirtHandlers

import (
	"errors"
	"net/http"
	"testing"
)

func TestVMGuestIdentityErrorMappings(t *testing.T) {
	createCases := []struct {
		err        string
		wantStatus int
		wantCode   string
	}{
		{"guest_identity_registry_initializing", http.StatusServiceUnavailable, "guest_identity_registry_initializing"},
		{"guest_identity_cluster_formation_in_progress", http.StatusServiceUnavailable, "guest_identity_registry_initializing"},
		{"cluster_consensus_unavailable: no quorum", http.StatusServiceUnavailable, "cluster_consensus_unavailable"},
		{"guest_identity_claim_conflict: stale token", http.StatusConflict, "guest_identity_claim_conflict"},
	}
	for _, test := range createCases {
		status, code := classifyCreateVMError(errors.New(test.err))
		if status != test.wantStatus || code != test.wantCode {
			t.Errorf("classifyCreateVMError(%q) = (%d, %q), want (%d, %q)", test.err, status, code, test.wantStatus, test.wantCode)
		}
	}

	removeCases := []struct {
		err        string
		wantStatus int
		wantCode   string
	}{
		{"guest_identity_registry_initializing", http.StatusServiceUnavailable, "guest_identity_registry_initializing"},
		{"guest_identity_cluster_formation_in_progress", http.StatusServiceUnavailable, "guest_identity_registry_initializing"},
		{"cluster_consensus_unavailable: no leader", http.StatusServiceUnavailable, "cluster_consensus_unavailable"},
		{"guest_identity_release_pending: raft shutdown", http.StatusServiceUnavailable, "cluster_consensus_unavailable"},
		{"guest_identity_claim_conflict: wrong owner", http.StatusConflict, "guest_identity_claim_conflict"},
		{"guest_delete_requires_backup_jobs_removed", http.StatusConflict, "guest_delete_requires_backup_jobs_removed"},
	}
	for _, test := range removeCases {
		status, code := classifyRemoveVMError(errors.New(test.err), "failed_to_remove_vm")
		if status != test.wantStatus || code != test.wantCode {
			t.Errorf("classifyRemoveVMError(%q) = (%d, %q), want (%d, %q)", test.err, status, code, test.wantStatus, test.wantCode)
		}
	}

	for _, errText := range []string{
		"guest_identity_registry_initializing",
		"guest_identity_cluster_formation_in_progress",
		"cluster_consensus_unavailable: no quorum",
	} {
		if status := vmTemplatePreflightStatusCode(errors.New(errText)); status != http.StatusServiceUnavailable {
			t.Errorf("vmTemplatePreflightStatusCode(%q) = %d, want 503", errText, status)
		}
	}
	if status := vmTemplatePreflightStatusCode(errors.New("guest_identity_claim_conflict: stale token")); status != http.StatusConflict {
		t.Errorf("vmTemplatePreflightStatusCode(claim conflict) = %d, want 409", status)
	}
}

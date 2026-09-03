// SPDX-License-Identifier: BSD-2-Clause

package jailHandlers

import (
	"errors"
	"net/http"
	"testing"
)

func TestJailGuestIdentityErrorMappings(t *testing.T) {
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
		status, code := classifyCreateJailError(errors.New(test.err))
		if status != test.wantStatus || code != test.wantCode {
			t.Errorf("classifyCreateJailError(%q) = (%d, %q), want (%d, %q)", test.err, status, code, test.wantStatus, test.wantCode)
		}
	}

	deleteCases := []struct {
		err        string
		wantStatus int
		wantCode   string
	}{
		{"guest_identity_registry_initializing", http.StatusServiceUnavailable, "guest_identity_registry_initializing"},
		{"guest_identity_cluster_formation_in_progress", http.StatusServiceUnavailable, "guest_identity_registry_initializing"},
		{"cluster_consensus_unavailable: no leader", http.StatusServiceUnavailable, "cluster_consensus_unavailable"},
		{"guest_identity_release_pending: raft shutdown", http.StatusServiceUnavailable, "cluster_consensus_unavailable"},
		{"guest_identity_claim_conflict: wrong owner", http.StatusConflict, "guest_identity_claim_conflict"},
	}
	for _, test := range deleteCases {
		status, code := classifyDeleteJailError(errors.New(test.err))
		if status != test.wantStatus || code != test.wantCode {
			t.Errorf("classifyDeleteJailError(%q) = (%d, %q), want (%d, %q)", test.err, status, code, test.wantStatus, test.wantCode)
		}
	}

	for _, errText := range []string{
		"guest_identity_registry_initializing",
		"guest_identity_cluster_formation_in_progress",
		"cluster_consensus_unavailable: no quorum",
	} {
		if status := jailTemplatePreflightStatusCode(errors.New(errText)); status != http.StatusServiceUnavailable {
			t.Errorf("jailTemplatePreflightStatusCode(%q) = %d, want 503", errText, status)
		}
	}
	if status := jailTemplatePreflightStatusCode(errors.New("guest_identity_claim_conflict: stale token")); status != http.StatusConflict {
		t.Errorf("jailTemplatePreflightStatusCode(claim conflict) = %d, want 409", status)
	}
}

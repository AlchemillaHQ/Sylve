// SPDX-License-Identifier: BSD-2-Clause

package clusterHandlers

import (
	"errors"
	"net/http"
	"testing"
)

func TestRestoreGuestIdentityErrorMappings(t *testing.T) {
	tests := []struct {
		err         string
		wantStatus  int
		wantMessage string
	}{
		{"guest_identity_claim_conflict: stale token", http.StatusConflict, "restore_guest_destination_conflict"},
		{"guest_identity_cluster_formation_in_progress", http.StatusServiceUnavailable, "restore_guest_identity_unavailable"},
		{"cluster_consensus_unavailable: no quorum", http.StatusServiceUnavailable, "restore_reservation_unavailable"},
	}
	for _, test := range tests {
		status, message := restoreFromTargetEnqueueError(errors.New(test.err))
		if status != test.wantStatus || message != test.wantMessage {
			t.Errorf("restoreFromTargetEnqueueError(%q) = (%d, %q), want (%d, %q)", test.err, status, message, test.wantStatus, test.wantMessage)
		}
	}
}

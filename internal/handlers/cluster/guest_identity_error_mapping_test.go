// SPDX-License-Identifier: BSD-2-Clause

package clusterHandlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/gin-gonic/gin"
)

func TestRestoreGuestIdentityErrorMappings(t *testing.T) {
	tests := []struct {
		err         string
		wantStatus  int
		wantMessage string
	}{
		{"guest_identity_claim_conflict: stale token", http.StatusConflict, "restore_guest_destination_conflict"},
		{"guest_identity_registry_initializing", http.StatusServiceUnavailable, "restore_guest_identity_unavailable"},
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

func TestGuestIdentityReclaimErrorMappings(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "canonical registration",
			err:         clusterModels.ErrGuestIdentityStillRegistered,
			wantStatus:  http.StatusConflict,
			wantMessage: "guest_identity_conflict",
		},
		{
			name:        "active guest operation",
			err:         errors.New("guest_operation_in_progress: restore"),
			wantStatus:  http.StatusConflict,
			wantMessage: "guest_identity_conflict",
		},
		{
			name:        "voter inventory unavailable",
			err:         fmt.Errorf("%w: inventory_unavailable", clusterModels.ErrGuestIdentityReclaimUnsafe),
			wantStatus:  http.StatusServiceUnavailable,
			wantMessage: "guest_identity_reclaim_unsafe",
		},
		{
			name:        "force confirmation",
			err:         fmt.Errorf("%w: confirmation_must_equal_guest_id", clusterModels.ErrGuestIdentityReclaimUnsafe),
			wantStatus:  http.StatusBadRequest,
			wantMessage: "guest_identity_reclaim_confirmation_required",
		},
	}

	gin.SetMode(gin.TestMode)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			writeGuestIdentityControlError(ctx, test.err)
			if recorder.Code != test.wantStatus || !strings.Contains(recorder.Body.String(), test.wantMessage) {
				t.Fatalf("response status=%d body=%s, want status=%d message=%s", recorder.Code, recorder.Body.String(), test.wantStatus, test.wantMessage)
			}
		})
	}
}

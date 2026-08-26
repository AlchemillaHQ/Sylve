// SPDX-License-Identifier: BSD-2-Clause

package zfsHandlers

import (
	"fmt"
	"net/http"
	"testing"

	zfsService "github.com/alchemillahq/sylve/internal/services/zfs"
)

func TestSnapshotCreationErrorResponse(t *testing.T) {
	tests := []struct {
		err         error
		wantStatus  int
		wantMessage string
	}{
		{fmt.Errorf("%w:ha_", zfsService.ErrReservedSnapshotNamespace), http.StatusBadRequest, "snapshot_namespace_reserved"},
		{fmt.Errorf("%w:guest_operation", zfsService.ErrSnapshotCreationBlocked), http.StatusConflict, "snapshot_creation_blocked"},
		{fmt.Errorf("zfs failed"), http.StatusInternalServerError, "internal_server_error"},
	}
	for _, test := range tests {
		status, message := snapshotCreationErrorResponse(test.err)
		if status != test.wantStatus || message != test.wantMessage {
			t.Fatalf("got (%d, %q), want (%d, %q)", status, message, test.wantStatus, test.wantMessage)
		}
	}
}

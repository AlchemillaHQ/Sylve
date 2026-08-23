// SPDX-License-Identifier: BSD-2-Clause

package zfsHandlers

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal/services/zfs"
)

func TestZFSErrorStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid", err: zfs.ErrInvalidRequest, want: http.StatusBadRequest},
		{name: "reserved snapshot namespace", err: zfs.ErrReservedSnapshotNamespace, want: http.StatusBadRequest},
		{name: "pool missing", err: zfs.ErrPoolNotFound, want: http.StatusNotFound},
		{name: "dataset missing", err: zfs.ErrDatasetNotFound, want: http.StatusNotFound},
		{name: "job missing", err: zfs.ErrSnapshotJobNotFound, want: http.StatusNotFound},
		{name: "source missing", err: zfs.ErrSourceNotFound, want: http.StatusNotFound},
		{name: "conflict", err: zfs.ErrConflict, want: http.StatusConflict},
		{name: "pool root", err: zfs.ErrCannotDeletePoolRootDataset, want: http.StatusConflict},
		{name: "snapshot blocked", err: zfs.ErrSnapshotCreationBlocked, want: http.StatusConflict},
		{name: "wrapped", err: fmt.Errorf("context: %w", zfs.ErrDatasetNotFound), want: http.StatusNotFound},
		{name: "unknown", err: errors.New("boom"), want: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := zfsErrorStatus(test.err); got != test.want {
				t.Fatalf("zfsErrorStatus(%v) = %d, want %d", test.err, got, test.want)
			}
		})
	}
}

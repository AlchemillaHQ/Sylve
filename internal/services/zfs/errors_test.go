// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"errors"
	"testing"

	"github.com/alchemillahq/gzfs"
)

func TestLookupErrorsOnlyClassifyMissingResources(t *testing.T) {
	missingDataset := &gzfs.CmdError{Stderr: "cannot open 'tank/missing': dataset does not exist"}
	if err := datasetLookupError(missingDataset, "dataset_not_found"); !errors.Is(err, ErrDatasetNotFound) {
		t.Fatalf("missing dataset error = %v, want ErrDatasetNotFound", err)
	}

	missingPool := errors.New(`pool with GUID "123" not found`)
	if err := poolLookupError(missingPool, "pool_not_found"); !errors.Is(err, ErrPoolNotFound) {
		t.Fatalf("missing pool error = %v, want ErrPoolNotFound", err)
	}

	commandFailure := &gzfs.CmdError{Stderr: "permission denied"}
	if err := datasetLookupError(commandFailure, "dataset_not_found"); err != commandFailure {
		t.Fatalf("dataset command failure was reclassified: %v", err)
	}
	if err := poolLookupError(commandFailure, "pool_not_found"); err != commandFailure {
		t.Fatalf("pool command failure was reclassified: %v", err)
	}
}

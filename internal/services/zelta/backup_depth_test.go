// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"slices"
	"testing"
)

func TestBackupZeltaArgsHonorsRecursiveSetting(t *testing.T) {
	t.Parallel()

	recursive := backupZeltaArgs("tank/source", "root@host:tank/target", "bk_j1_test", true)
	if slices.Contains(recursive, "--depth") {
		t.Fatalf("recursive backup unexpectedly limited depth: %v", recursive)
	}

	nonrecursive := backupZeltaArgs("tank/source", "root@host:tank/target", "bk_j1_test", false)
	wantPair := false
	for i := 0; i+1 < len(nonrecursive); i++ {
		if nonrecursive[i] == "--depth" && nonrecursive[i+1] == "1" {
			wantPair = true
			break
		}
	}
	if !wantPair {
		t.Fatalf("nonrecursive backup did not set --depth 1: %v", nonrecursive)
	}
	if got := nonrecursive[len(nonrecursive)-2:]; !slices.Equal(got, []string{"tank/source", "root@host:tank/target"}) {
		t.Fatalf("source/target arguments moved: got %v", got)
	}
}

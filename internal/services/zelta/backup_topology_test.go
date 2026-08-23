// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"testing"
)

func TestBackupTopologyDetectsAddedRemovedAndRetypedDatasets(t *testing.T) {
	base := []backupTopologyEntry{
		{Suffix: "", Type: "filesystem"},
		{Suffix: "child", Type: "filesystem"},
	}
	tests := []struct {
		name  string
		other []backupTopologyEntry
		want  bool
	}{
		{name: "same", other: append([]backupTopologyEntry(nil), base...), want: true},
		{name: "added", other: append(append([]backupTopologyEntry(nil), base...), backupTopologyEntry{Suffix: "new", Type: "filesystem"})},
		{name: "removed", other: base[:1]},
		{name: "retyped", other: []backupTopologyEntry{{Suffix: "", Type: "filesystem"}, {Suffix: "child", Type: "volume"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := backupTopologiesEqual(base, tt.other); got != tt.want {
				t.Fatalf("got %t want %t", got, tt.want)
			}
		})
	}
}

func TestBackupTopologyEntriesMapRenamedTargetToStableSuffixes(t *testing.T) {
	source, err := backupTopologyEntries(map[string]string{
		"pool/source":       "filesystem",
		"pool/source/child": "volume",
	}, "pool/source")
	if err != nil {
		t.Fatal(err)
	}
	target, err := backupTopologyEntries(map[string]string{
		"backup/job/active":       "filesystem",
		"backup/job/active/child": "volume",
	}, "backup/job/active")
	if err != nil {
		t.Fatal(err)
	}
	if !backupTopologiesEqual(source, target) {
		t.Fatalf("renamed equivalent topology mismatch: source=%v target=%v", source, target)
	}
}

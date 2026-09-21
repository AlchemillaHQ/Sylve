// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"errors"
	"testing"
)

func TestIsZFSDatasetDependentCloneError(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "unrelated", err: errors.New("dataset is busy"), want: false},
		{
			name: "filesystem dependent clones",
			err:  errors.New("cannot destroy 'tank/sylve/bootstraps/15-0-Base': filesystem has dependent clones"),
			want: true,
		},
		{
			name: "snapshot dependent clones",
			err:  errors.New("cannot destroy snapshot: dataset has dependent clones"),
			want: true,
		},
		{
			name: "case insensitive",
			err:  errors.New("DATASET HAS DEPENDENT CLONES"),
			want: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsZFSDatasetDependentCloneError(test.err); got != test.want {
				t.Fatalf("IsZFSDatasetDependentCloneError(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

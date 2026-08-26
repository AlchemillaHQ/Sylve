// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"testing"

	"github.com/alchemillahq/gzfs"
)

func TestAggregatePoolHealth(t *testing.T) {
	testCases := []struct {
		name    string
		states  []string
		missing bool
		want    string
	}{
		{name: "online", states: []string{string(gzfs.ZPoolStateOnline)}, want: string(gzfs.ZPoolStateOnline)},
		{name: "worst known state", states: []string{string(gzfs.ZPoolStateOnline), string(gzfs.ZPoolStateDegraded)}, want: string(gzfs.ZPoolStateDegraded)},
		{name: "critical beats degraded", states: []string{string(gzfs.ZPoolStateDegraded), string(gzfs.ZPoolStateFaulted)}, want: string(gzfs.ZPoolStateFaulted)},
		{name: "corrupt data beats degraded", states: []string{string(gzfs.ZPoolStateDegraded), string(gzfs.ZPoolStateCorruptData)}, want: string(gzfs.ZPoolStateCorruptData)},
		{name: "missing is unknown", states: []string{string(gzfs.ZPoolStateOnline)}, missing: true, want: PoolHealthUnknown},
		{name: "known failure beats missing", states: []string{string(gzfs.ZPoolStateFaulted)}, missing: true, want: string(gzfs.ZPoolStateFaulted)},
		{name: "empty is unknown", want: PoolHealthUnknown},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := aggregatePoolHealth(testCase.states, testCase.missing); got != testCase.want {
				t.Fatalf("aggregatePoolHealth() = %q, want %q", got, testCase.want)
			}
		})
	}
}

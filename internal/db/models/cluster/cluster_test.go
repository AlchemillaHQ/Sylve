// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import "testing"

func TestClusterHasIncompleteJoin(t *testing.T) {
	tests := []struct {
		name    string
		cluster Cluster
		want    bool
	}{
		{name: "empty"},
		{name: "blank leader", cluster: Cluster{JoinLeaderIP: " "}},
		{name: "joining", cluster: Cluster{JoinLeaderIP: "192.0.2.1"}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.cluster.HasIncompleteJoin(); got != test.want {
				t.Fatalf("HasIncompleteJoin() = %t, want %t", got, test.want)
			}
		})
	}
}

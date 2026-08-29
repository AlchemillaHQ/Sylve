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
		{name: "intent without phase", cluster: Cluster{JoinNodeID: "node-2"}, want: true},
		{
			name:    "active",
			cluster: Cluster{JoinNodeID: "node-2", JoinPhase: "catching_up"},
			want:    true,
		},
		{
			name:    "failed",
			cluster: Cluster{JoinNodeID: "node-2", JoinPhase: "failed"},
			want:    true,
		},
		{
			name:    "complete",
			cluster: Cluster{JoinNodeID: "node-2", JoinPhase: JoinPhaseComplete},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.cluster.HasIncompleteJoin(); got != test.want {
				t.Fatalf("HasIncompleteJoin() = %t, want %t", got, test.want)
			}
		})
	}
}

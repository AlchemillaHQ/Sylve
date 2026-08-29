// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestCommandStatusReportsOnlyIncompleteJoinPhase(t *testing.T) {
	tests := []struct {
		name      string
		phase     string
		wantPhase string
	}{
		{name: "complete", phase: JoinPhaseComplete},
		{name: "active", phase: JoinPhaseCatchingUp, wantPhase: JoinPhaseCatchingUp},
		{name: "failed", phase: JoinPhaseFailed, wantPhase: JoinPhaseFailed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := newClusterServiceTestDB(t, &clusterModels.Cluster{})
			if err := database.Create(&clusterModels.Cluster{
				Enabled: true, RaftIP: "192.0.2.10", JoinNodeID: "node-2", JoinPhase: test.phase,
			}).Error; err != nil {
				t.Fatal(err)
			}
			status, err := (&Service{DB: database, NodeID: "node-2"}).CommandStatus()
			if err != nil {
				t.Fatal(err)
			}
			if status.JoinPhase != test.wantPhase {
				t.Fatalf("JoinPhase = %q, want %q", status.JoinPhase, test.wantPhase)
			}
		})
	}
}

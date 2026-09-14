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
	"time"

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

func TestClusterInspectionDoesNotReportLeaderWithoutQuorum(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.Cluster{}, &clusterModels.ClusterNode{})
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	if err := leader.service.DB.Create(&clusterModels.Cluster{Enabled: true}).Error; err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	for _, node := range nodes {
		if err := leader.service.DB.Create(&clusterModels.ClusterNode{
			NodeUUID: node.id, Hostname: node.id, Status: nodeStatusOnline,
		}).Error; err != nil {
			t.Fatalf("seed cluster node: %v", err)
		}
		if node != leader {
			leader.transport.Disconnect(node.addr)
			node.transport.Disconnect(leader.addr)
		}
	}

	status, err := leader.service.CommandStatus()
	if err != nil {
		t.Fatalf("cluster status: %v", err)
	}
	if status.LeaderID != "" || status.LeaderAddress != "" || !status.Partial {
		t.Fatalf("unverified leader view = %+v, want no leader and partial=true", status)
	}

	members, err := leader.service.CommandMembers()
	if err != nil {
		t.Fatalf("cluster members: %v", err)
	}
	for _, member := range members {
		if member.IsLeader || member.Status != "unknown" {
			t.Fatalf("unverified member view = %+v, want no leader and unknown status", member)
		}
	}
}

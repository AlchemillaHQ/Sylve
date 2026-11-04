// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import (
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

func TestRecoverRaftQuorumKeepsOnlyListedVoters(t *testing.T) {
	raftDir := t.TempDir()
	voters := []raft.Server{
		{ID: "node-a", Address: "192.0.2.1:8180", Suffrage: raft.Voter},
		{ID: "node-b", Address: "192.0.2.2:8180", Suffrage: raft.Voter},
		{ID: "node-c", Address: "192.0.2.3:8180", Suffrage: raft.Voter},
	}
	bootstrapOfflineRaft(t, raftDir, raft.Configuration{Servers: voters})

	database := testutil.NewSQLiteTestDB(t,
		&clusterModels.Cluster{}, &clusterModels.ClusterNote{}, &clusterModels.ClusterOption{},
		&clusterModels.BackupTarget{}, &clusterModels.BackupJob{}, &clusterModels.BackupJobOperation{},
		&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{}, &clusterModels.ReplicationGuestOperation{},
		&clusterModels.ReplicationGuestOperationReceipt{}, &clusterModels.ClusterSSHIdentity{},
		&clusterModels.EncryptionKey{},
	)
	if err := database.Create(&clusterModels.Cluster{Enabled: true, RaftIP: "192.0.2.1"}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := recoverRaftQuorum(database, raftDir, "node-a", []string{"node-a", "node-b"})
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !slices.Equal(result.Voters, []string{"node-a", "node-b"}) || !slices.Equal(result.Removed, []string{"node-c"}) {
		t.Fatalf("result = %+v", result)
	}

	logs, err := raftboltdb.NewBoltStore(filepath.Join(raftDir, "raft-log.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	snapshots, err := raft.NewFileSnapshotStore(raftDir, 2, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := latestOfflineRaftConfiguration(logs, snapshots)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(configuration.Servers, voters[:2]) {
		t.Fatalf("configuration after recovery = %+v", configuration.Servers)
	}
}

func TestSurvivingRaftConfigurationRefusesBadVoterLists(t *testing.T) {
	current := raft.Configuration{Servers: []raft.Server{
		{ID: "node-a", Suffrage: raft.Voter},
		{ID: "node-b", Suffrage: raft.Voter},
		{ID: "node-c", Suffrage: raft.Nonvoter},
	}}
	for _, test := range []struct {
		name   string
		voters []string
		want   string
	}{
		{"local node missing", []string{"node-b"}, "local_node_not_listed"},
		{"unknown voter", []string{"node-a", "node-x"}, "unknown_voters: node-x"},
		{"nonvoter listed", []string{"node-a", "node-c"}, "not_a_voter: node-c"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := survivingRaftConfiguration(current, "node-a", test.voters)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %s", err, test.want)
			}
		})
	}

	healthy := raft.Configuration{Servers: current.Servers[:2]}
	if _, _, err := survivingRaftConfiguration(healthy, "node-a", []string{"node-a", "node-b"}); err == nil ||
		!strings.Contains(err.Error(), "nothing_to_remove") {
		t.Fatalf("listing every voter: error = %v", err)
	}
}

func bootstrapOfflineRaft(t *testing.T, raftDir string, configuration raft.Configuration) {
	t.Helper()
	logs, err := raftboltdb.NewBoltStore(filepath.Join(raftDir, "raft-log.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	stable, err := raftboltdb.NewBoltStore(filepath.Join(raftDir, "raft-stable.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer stable.Close()
	snapshots, err := raft.NewFileSnapshotStore(raftDir, 2, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	conf := raft.DefaultConfig()
	conf.LocalID = configuration.Servers[0].ID
	conf.Logger = hclog.NewNullLogger()
	_, transport := raft.NewInmemTransport(configuration.Servers[0].Address)
	if err := raft.BootstrapCluster(conf, logs, stable, snapshots, transport, configuration); err != nil {
		t.Fatal(err)
	}
}

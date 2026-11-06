// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestMigrateLegacyPortsOnlyMovesTheLocalRaftPort(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{}, &clusterModels.ClusterSSHIdentity{})
	s := withFSM(&Service{DB: db})

	clusterRecord := clusterModels.Cluster{RaftPort: 7000}
	if err := db.Create(&clusterRecord).Error; err != nil {
		t.Fatalf("failed to seed cluster record: %v", err)
	}
	legacy := clusterModels.ClusterSSHIdentity{
		NodeUUID: "node-b", SSHUser: "root", SSHHost: "10.0.0.11", SSHPort: 8122, PublicKey: "pk-b",
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("failed to seed cluster ssh identity: %v", err)
	}

	if err := s.MigrateLegacyPorts(); err != nil {
		t.Fatalf("MigrateLegacyPorts returned error: %v", err)
	}

	var updatedCluster clusterModels.Cluster
	if err := db.First(&updatedCluster, clusterRecord.ID).Error; err != nil {
		t.Fatalf("failed to fetch migrated cluster record: %v", err)
	}
	if updatedCluster.RaftPort != ClusterRaftPort {
		t.Fatalf("expected raft port %d, got %d", ClusterRaftPort, updatedCluster.RaftPort)
	}
	var identity clusterModels.ClusterSSHIdentity
	if err := db.First(&identity, "node_uuid = ?", "node-b").Error; err != nil {
		t.Fatal(err)
	}
	if identity.SSHPort != 8122 {
		t.Fatalf("replicated identity rewritten locally: port %d", identity.SSHPort)
	}
}

func TestMigrateLegacyPortsFailsWhenClusterRecordMissing(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{}, &clusterModels.ClusterSSHIdentity{})
	s := withFSM(&Service{DB: db})

	err := s.MigrateLegacyPorts()
	if err == nil {
		t.Fatal("expected migration error when cluster record is missing, got nil")
	}
	if !strings.Contains(err.Error(), "failed_to_load_cluster_record") {
		t.Fatalf("expected failed_to_load_cluster_record error, got: %v", err)
	}
}

func TestMigrateLegacyPortsFailsWhenServiceUnavailable(t *testing.T) {
	s := &Service{}

	err := s.MigrateLegacyPorts()
	if err == nil {
		t.Fatal("expected service unavailable error, got nil")
	}
	if !strings.Contains(err.Error(), "cluster_service_unavailable") {
		t.Fatalf("expected cluster_service_unavailable error, got: %v", err)
	}
}

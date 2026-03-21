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

func TestMigrateLegacyPortsRewritesRaftAndSSHPorts(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{}, &clusterModels.ClusterSSHIdentity{})
	s := &Service{DB: db}

	clusterRecord := clusterModels.Cluster{
		Enabled:  false,
		Key:      "",
		RaftIP:   "",
		RaftPort: 7000,
	}
	if err := db.Create(&clusterRecord).Error; err != nil {
		t.Fatalf("failed to seed cluster record: %v", err)
	}

	identities := []clusterModels.ClusterSSHIdentity{
		{NodeUUID: "node-a", SSHUser: "root", SSHHost: "10.0.0.10", SSHPort: 0, PublicKey: "pk-a"},
		{NodeUUID: "node-b", SSHUser: "root", SSHHost: "10.0.0.11", SSHPort: legacyClusterEmbeddedSSHPort, PublicKey: "pk-b"},
		{NodeUUID: "node-c", SSHUser: "root", SSHHost: "10.0.0.12", SSHPort: 2222, PublicKey: "pk-c"},
	}
	if err := db.Create(&identities).Error; err != nil {
		t.Fatalf("failed to seed cluster ssh identities: %v", err)
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

	var migrated []clusterModels.ClusterSSHIdentity
	if err := db.Order("node_uuid ASC").Find(&migrated).Error; err != nil {
		t.Fatalf("failed to fetch migrated cluster ssh identities: %v", err)
	}
	if len(migrated) != 3 {
		t.Fatalf("expected 3 migrated identities, got %d", len(migrated))
	}

	for _, identity := range migrated {
		switch identity.NodeUUID {
		case "node-a", "node-b":
			if identity.SSHPort != ClusterEmbeddedSSHPort {
				t.Fatalf("expected %s SSH port to be migrated to %d, got %d", identity.NodeUUID, ClusterEmbeddedSSHPort, identity.SSHPort)
			}
		case "node-c":
			if identity.SSHPort != 2222 {
				t.Fatalf("expected non-legacy SSH port to remain 2222, got %d", identity.SSHPort)
			}
		default:
			t.Fatalf("unexpected node UUID in migrated identities: %s", identity.NodeUUID)
		}
	}
}

func TestMigrateLegacyPortsFailsWhenClusterRecordMissing(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{}, &clusterModels.ClusterSSHIdentity{})
	s := &Service{DB: db}

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

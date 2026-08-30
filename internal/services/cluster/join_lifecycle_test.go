// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestJoinIntentPersistsRecoveryInputsAndResetClearsThem(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	if err := db.Create(&clusterModels.Cluster{
		Enabled: false, Key: "standalone-key", RaftPort: ClusterRaftPort,
	}).Error; err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	service := &Service{DB: db, NodeID: "joining-node", mutationGate: newOpenTestMutationGate(t)}
	report := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
		NodeID: "joining-node", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 42, RecordID: 7, Name: "vm-42",
	}})
	request := JoinAdmissionRequest{
		NodeID: "joining-node", NodeIP: "192.0.2.20", NodeVersion: "1.2.3", Inventory: report,
	}
	if err := service.SaveJoinIntent("192.0.2.10", "cluster-key", request); err != nil {
		t.Fatalf("save join intent: %v", err)
	}

	var persisted clusterModels.Cluster
	if err := db.First(&persisted).Error; err != nil {
		t.Fatalf("load join intent: %v", err)
	}
	if persisted.Enabled {
		t.Fatal("saving intent enabled clustering before local Raft startup")
	}
	if persisted.Key != "cluster-key" || persisted.JoinLeaderIP != "192.0.2.10" ||
		persisted.JoinNodeID != "joining-node" || persisted.JoinNodeIP != "192.0.2.20" ||
		persisted.JoinNodeVersion != "1.2.3" || persisted.JoinPhase != JoinPhaseIntentSaved {
		t.Fatalf("persisted join intent = %+v", persisted)
	}
	var persistedReport GuestIdentityInventoryReport
	if err := json.Unmarshal(persisted.JoinInventory, &persistedReport); err != nil {
		t.Fatalf("decode persisted inventory: %v", err)
	}
	if persistedReport.Digest != report.Digest {
		t.Fatalf("persisted inventory digest = %s, want %s", persistedReport.Digest, report.Digest)
	}

	restarted := &Service{DB: db, NodeID: "joining-node", mutationGate: newOpenTestMutationGate(t)}
	status, err := restarted.JoinStatus()
	if err != nil {
		t.Fatalf("join status after restart: %v", err)
	}
	if status.Phase != JoinPhaseIntentSaved || status.NodeID != "joining-node" || !status.Retrying {
		t.Fatalf("join status after restart = %+v", status)
	}

	if err := restarted.MarkJoinIntentPhase(JoinPhaseStalled, errors.New("temporary outage")); err != nil {
		t.Fatalf("mark stalled intent: %v", err)
	}
	status, err = restarted.JoinStatus()
	if err != nil {
		t.Fatalf("stalled join status: %v", err)
	}
	if status.Phase != JoinPhaseStalled || status.LastError != "temporary outage" || !status.Retrying {
		t.Fatalf("stalled join status = %+v", status)
	}

	if err := restarted.MarkDeclustered(); err != nil {
		t.Fatalf("clear cluster state: %v", err)
	}
	if err := db.First(&persisted).Error; err != nil {
		t.Fatalf("reload reset cluster: %v", err)
	}
	if persisted.JoinNodeID != "" || persisted.JoinLeaderIP != "" || len(persisted.JoinInventory) != 0 ||
		persisted.JoinPhase != "" || persisted.JoinLastError != "" {
		t.Fatalf("reset retained join intent: %+v", persisted)
	}
}

func TestJoinIntentRejectsIPv6WithoutPersisting(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	if err := db.Create(&clusterModels.Cluster{RaftPort: ClusterRaftPort}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: db, NodeID: "joining-node"}
	request := JoinAdmissionRequest{
		NodeID: "joining-node", NodeIP: "192.0.2.20", NodeVersion: "1.2.3",
		Inventory: BuildGuestIdentityInventoryReport(nil),
	}
	if err := service.SaveJoinIntent("2001:db8::10", "cluster-key", request); err == nil ||
		err.Error() != "cluster_ipv6_unsupported" {
		t.Fatalf("leader IPv6 error = %v", err)
	}
	request.NodeIP = "2001:db8::20"
	if err := service.SaveJoinIntent("192.0.2.10", "cluster-key", request); err == nil ||
		err.Error() != "cluster_ipv6_unsupported" {
		t.Fatalf("node IPv6 error = %v", err)
	}
	var record clusterModels.Cluster
	if err := db.First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.JoinPhase != "" || record.JoinLeaderIP != "" || record.JoinNodeIP != "" {
		t.Fatalf("IPv6 join intent was persisted: %+v", record)
	}
}

func TestLeaderIPFromNotLeaderError(t *testing.T) {
	tests := map[string]string{
		"not_leader; leader_addr=10.1.32.230:8180; leader_id=node-a": "10.1.32.230",
		"not_leader; leader_addr=[fd00::10]:8180; leader_id=node-b":  "fd00::10",
		"not_leader; leader_addr=hostname:8180; leader_id=node-c":    "",
		"unrelated": "",
	}
	for input, expected := range tests {
		if actual := leaderIPFromNotLeaderError(input); actual != expected {
			t.Errorf("leaderIPFromNotLeaderError(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestSubmitJoinIntentKeepsRetryableTransportFailure(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	if err := db.Create(&clusterModels.Cluster{
		Enabled: true, RaftPort: ClusterRaftPort,
	}).Error; err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	service := &Service{DB: db, NodeID: "joining-node", mutationGate: newOpenTestMutationGate(t)}
	request := JoinAdmissionRequest{
		NodeID: "joining-node", NodeIP: "192.0.2.20", NodeVersion: "1.2.3",
		Inventory: BuildGuestIdentityInventoryReport(nil),
	}
	if err := service.SaveJoinIntent("127.0.0.254", "cluster-key", request); err != nil {
		t.Fatalf("save intent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := service.SubmitJoinIntent(ctx)
	if result.Err == nil || !result.Retryable {
		t.Fatalf("submission result = %+v, want retryable transport failure", result)
	}
	if result.Status.Phase != JoinPhaseStalled || !result.Status.Retrying {
		t.Fatalf("join status = %+v", result.Status)
	}

	var persisted clusterModels.Cluster
	if err := db.First(&persisted).Error; err != nil {
		t.Fatalf("reload intent: %v", err)
	}
	if persisted.JoinAttempts != 1 || persisted.JoinPhase != JoinPhaseStalled ||
		len(persisted.JoinInventory) == 0 || persisted.JoinLastError == "" {
		t.Fatalf("retryable failure was not retained: %+v", persisted)
	}
}

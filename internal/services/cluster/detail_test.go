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
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestDetail(t *testing.T) {
	s := &Service{}
	detail := s.Detail()
	if detail == nil {
		t.Fatal("expected non-nil detail")
	}
	if detail.NodeID == "" {
		t.Fatal("expected non-empty NodeID from system UUID")
	}
	if detail.Hostname == "" {
		t.Fatal("expected non-empty Hostname")
	}
	if detail.APIPort != ClusterEmbeddedHTTPSPort {
		t.Fatalf("expected APIPort=%d, got %d", ClusterEmbeddedHTTPSPort, detail.APIPort)
	}
}

func TestLocalNodeIDPrefersConfiguredNodeID(t *testing.T) {
	service := &Service{NodeID: "  configured-node-id  "}
	if got := service.LocalNodeID(); got != "configured-node-id" {
		t.Fatalf("LocalNodeID()=%q, want configured node ID", got)
	}
}

func TestLocalNodeIDFallsBackToSystemUUID(t *testing.T) {
	service := &Service{}
	detail := service.Detail()
	if detail == nil {
		t.Fatal("expected non-nil detail")
	}
	if got := service.LocalNodeID(); got != detail.NodeID {
		t.Fatalf("LocalNodeID()=%q, want system UUID %q", got, detail.NodeID)
	}
}

func TestResourcesContextStandaloneNeedsNoClusterCredential(t *testing.T) {
	service := &Service{DB: newClusterServiceTestDB(t, &clusterModels.ClusterNode{})}
	resources, err := service.ResourcesContext(context.Background())
	if err != nil {
		t.Fatalf("standalone resources: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("resources=%+v, want empty", resources)
	}
}

func TestResourcesContextSkipsKnownOfflinePeer(t *testing.T) {
	peer := newClusterPeerSimulator()
	defer peer.Close()

	db := newClusterServiceTestDB(t, &clusterModels.ClusterNode{})
	node := clusterModels.ClusterNode{
		NodeUUID: "known-offline-peer",
		Hostname: "offline-peer",
		API:      peer.Addr(),
		Status:   nodeStatusOffline,
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("seed offline peer: %v", err)
	}

	service := &Service{DB: db, AuthService: clusterAuthStub{}}
	resources, err := service.ResourcesContext(context.Background())
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(resources) != 1 || resources[0].NodeUUID != node.NodeUUID || resources[0].Hostname != node.Hostname {
		t.Fatalf("offline peer identity missing: %+v", resources)
	}
	if peer.NumRequests() != 0 {
		t.Fatalf("known offline peer received %d inventory requests", peer.NumRequests())
	}
}

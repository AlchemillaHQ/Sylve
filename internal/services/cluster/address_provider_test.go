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

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

func TestRaftAddressProviderOverrideLifecycle(t *testing.T) {
	provider := newRaftAddressProvider()
	nodeID := raft.ServerID("node-2")

	if _, err := provider.ServerAddr(nodeID); err == nil {
		t.Fatal("expected an absent override to use Raft's configured-address fallback")
	}

	provider.set(nodeID, raft.ServerAddress("192.0.2.20:8180"))
	address, err := provider.ServerAddr(nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if address != raft.ServerAddress("192.0.2.20:8180") {
		t.Fatalf("address = %q", address)
	}

	provider.clear(nodeID)
	if _, err := provider.ServerAddr(nodeID); err == nil {
		t.Fatal("expected fallback after clearing the override")
	}
}

func TestRaftTransportUsesFallbackAndTemporaryOverride(t *testing.T) {
	provider := newRaftAddressProvider()
	transport := newTestTCPTransport(t, provider)
	peer := newTestTCPTransport(t, nil)
	serveAppendEntries(t, peer, 2)

	request := &raft.AppendEntriesRequest{Term: 1}
	response := &raft.AppendEntriesResponse{}
	if err := transport.AppendEntries("peer", peer.LocalAddr(), request, response); err != nil {
		t.Fatalf("configured-address fallback failed: %v", err)
	}

	provider.set("peer", peer.LocalAddr())
	response = &raft.AppendEntriesResponse{}
	if err := transport.AppendEntries("peer", "127.0.0.1:1", request, response); err != nil {
		t.Fatalf("temporary override failed: %v", err)
	}
}

func newTestTCPTransport(t *testing.T, provider raft.ServerAddressProvider) *raft.NetworkTransport {
	t.Helper()
	transport, err := raft.NewTCPTransportWithConfig("127.0.0.1:0", nil, &raft.NetworkTransportConfig{
		ServerAddressProvider: provider,
		MaxPool:               1,
		Timeout:               time.Second,
		Logger:                hclog.NewNullLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	return transport
}

func serveAppendEntries(t *testing.T, transport *raft.NetworkTransport, count int) {
	t.Helper()
	go func() {
		for range count {
			select {
			case rpc := <-transport.Consumer():
				rpc.Respond(&raft.AppendEntriesResponse{Term: 1, Success: true}, nil)
			case <-time.After(2 * time.Second):
				return
			}
		}
	}()
}

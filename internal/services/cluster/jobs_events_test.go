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
	hub "github.com/alchemillahq/sylve/internal/events"
)

func TestStatusFromHealth(t *testing.T) {
	if got := statusFromHealth(true); got != "online" {
		t.Fatalf("expected online, got %q", got)
	}
	if got := statusFromHealth(false); got != "offline" {
		t.Fatalf("expected offline, got %q", got)
	}
}

func TestPreferredHostname(t *testing.T) {
	tests := []struct {
		name string
		cur  curInfo
		want string
	}{
		{"canon wins", curInfo{canonHost: "canon.example.com", rawHost: "10.0.0.1"}, "canon.example.com"},
		{"raw fallback", curInfo{canonHost: "", rawHost: "10.0.0.1"}, "10.0.0.1"},
		{"both empty", curInfo{canonHost: "", rawHost: ""}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := preferredHostname(tt.cur); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestRaftAddressHost(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{"192.168.1.1:8180", "192.168.1.1"},
		{"node-1", "node-1"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got := raftAddressHost(tt.addr)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestApplyProbeHysteresis(t *testing.T) {
	db := newClusterServiceTestDB(t)
	s := &Service{DB: db}
	s.peerProbeFailureStreak = make(map[string]int)

	if got := s.applyProbeHysteresis("peer-1", "online"); got != "online" {
		t.Fatalf("expected online, got %q", got)
	}
	if got := s.applyProbeHysteresis("peer-2", "offline"); got != "online" {
		t.Fatalf("expected online via hysteresis on first offline, got %q", got)
	}
	if got := s.applyProbeHysteresis("peer-2", "offline"); got != "offline" {
		t.Fatalf("expected offline after threshold (streak=2), got %q", got)
	}
	if got := s.applyProbeHysteresis("peer-2", "online"); got != "online" {
		t.Fatalf("expected online after returning, got %q", got)
	}
}

func TestHasSignificantChange(t *testing.T) {
	cur := curInfo{
		api: "10.0.0.1:8184", canonHost: "host.example.com", healthOK: true,
		cpu: 8, cpuUsage: 10.0, memory: 8192, memUsage: 20.0,
		disk: 102400, diskUsage: 30.0, guestIDs: []uint{1, 2},
	}

	ex := clusterModels.ClusterNode{
		Status: "online", API: "10.0.0.1:8184", Hostname: "host.example.com",
		CPU: 8, CPUUsage: 10.0, Memory: 8192, MemoryUsage: 20.0,
		Disk: 102400, DiskUsage: 30.0, GuestIDs: []uint{1, 2},
	}

	if hasSignificantChange(cur, ex) {
		t.Fatal("identical should not be significant")
	}

	exOffline := ex
	exOffline.Status = "offline"
	if !hasSignificantChange(cur, exOffline) {
		t.Fatal("status change should be significant")
	}

	exAPI := ex
	exAPI.API = "10.0.0.2:8184"
	if !hasSignificantChange(cur, exAPI) {
		t.Fatal("API change should be significant")
	}

	exHost := ex
	exHost.Hostname = "other.example.com"
	if !hasSignificantChange(cur, exHost) {
		t.Fatal("hostname change should be significant")
	}

	exGuests := ex
	exGuests.GuestIDs = []uint{1, 2, 3}
	if !hasSignificantChange(cur, exGuests) {
		t.Fatal("guest IDs change should be significant")
	}

	exCPU := ex
	exCPU.CPUUsage = 11.1
	if !hasSignificantChange(cur, exCPU) {
		t.Fatal("large CPU usage change should be significant")
	}

	exCPUSmall := ex
	exCPUSmall.CPUUsage = 10.5
	if hasSignificantChange(cur, exCPUSmall) {
		t.Fatal("small CPU usage change should not be significant")
	}
}

func TestEmitLeftPanelRefreshLocal(t *testing.T) {
	db := newClusterServiceTestDB(t)
	service := &Service{DB: db}

	ch, unsub := hub.SSE.Subscribe()
	defer unsub()

	done := make(chan hub.Event, 1)
	go func() {
		defer func() { recover() }()
		for evt := range ch {
			done <- evt
			return
		}
	}()

	service.EmitLeftPanelRefreshLocal("test-reason")

	select {
	case evt := <-done:
		if evt.Type != "left-panel-refresh" {
			t.Fatalf("expected left-panel-refresh event, got %q", evt.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for left-panel-refresh event")
	}
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestApplyOfflineClusterIPRecoveryPreservesStateAndIsIdempotent(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &clusterModels.Cluster{}, &clusterModels.ClusterNote{})
	if err := database.Create(&clusterModels.Cluster{Enabled: true, RaftIP: "192.0.2.10"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&clusterModels.ClusterNote{Title: "keep", Content: "retained"}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := applyOfflineClusterIPRecovery(database, "node-2", "192.0.2.20")
	if err != nil {
		t.Fatal(err)
	}
	if result.OldIP != "192.0.2.10" || result.NewIP != "192.0.2.20" || result.AlreadySet {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.RepairCommand, "--allow-disruption") {
		t.Fatalf("repair command = %q", result.RepairCommand)
	}

	var record clusterModels.Cluster
	if err := database.First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.RaftIP != "192.0.2.20" || record.ReaddressOldIP != "192.0.2.10" ||
		record.ReaddressNewIP != "192.0.2.20" || record.ReaddressPhase != offlineReaddressPhaseLocalRebound {
		t.Fatalf("record = %+v", record)
	}
	var note clusterModels.ClusterNote
	if err := database.First(&note).Error; err != nil || note.Title != "keep" || note.Content != "retained" {
		t.Fatalf("note = %+v, err = %v", note, err)
	}

	repeated, err := applyOfflineClusterIPRecovery(database, "node-2", "192.0.2.20")
	if err != nil {
		t.Fatal(err)
	}
	if !repeated.AlreadySet || repeated.OldIP != "192.0.2.10" {
		t.Fatalf("repeated result = %+v", repeated)
	}
	if _, err := applyOfflineClusterIPRecovery(database, "node-2", "192.0.2.30"); err == nil {
		t.Fatal("expected a conflicting pending recovery to fail")
	}
}

func TestRequireOfflineConsoleDistinguishesLiveAndAbsentSocket(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "dcs")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	socketPath := filepath.Join(directory, "console.sock")
	if err := requireOfflineConsole(socketPath); err != nil {
		t.Fatalf("absent socket: %v", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := requireOfflineConsole(socketPath); err == nil || err.Error() != "cluster_readdress_daemon_must_be_stopped" {
		t.Fatalf("live socket error = %v", err)
	}
}

func TestNormalizeOfflineClusterIPRejectsIPv6(t *testing.T) {
	if _, err := normalizeOfflineClusterIP("2001:db8::20"); err == nil || err.Error() != "cluster_readdress_ipv6_unsupported" {
		t.Fatalf("IPv6 error = %v", err)
	}
	if _, err := normalizeOfflineClusterIP("127.0.0.1"); err == nil || err.Error() != "cluster_readdress_ip_invalid" {
		t.Fatalf("loopback error = %v", err)
	}
	if got, err := normalizeOfflineClusterIP("192.0.2.20"); err != nil || got != "192.0.2.20" {
		t.Fatalf("IPv4 = %q, error = %v", got, err)
	}
}

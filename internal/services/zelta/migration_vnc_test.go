// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"net"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func listenOnEphemeralTCPPort(t *testing.T) (int, net.Listener) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on ephemeral TCP port: %v", err)
	}
	return listener.Addr().(*net.TCPAddr).Port, listener
}

func TestResolveMigratedVMVNCPortDetectsLiveSocket(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{})
	service := &Service{DB: db}
	requestedPort, listener := listenOnEphemeralTCPPort(t)
	defer listener.Close()

	resolvedPort, reassigned, err := service.resolveMigratedVMVNCPort(107, requestedPort)
	if err != nil {
		t.Fatalf("resolve occupied VNC port: %v", err)
	}
	if !reassigned || resolvedPort == requestedPort {
		t.Fatalf(
			"occupied port resolution = port %d reassigned %v, want a different port",
			resolvedPort,
			reassigned,
		)
	}
	if resolvedPort < 5900 || resolvedPort > 65535 {
		t.Fatalf("resolved VNC port %d is outside the allocation range", resolvedPort)
	}
}

func TestResolveMigratedVMVNCPortDetectsConfiguredPort(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{})
	service := &Service{DB: db}
	requestedPort, listener := listenOnEphemeralTCPPort(t)
	if err := listener.Close(); err != nil {
		t.Fatalf("release requested VNC port: %v", err)
	}
	if err := db.Create(&vmModels.VM{
		RID:        106,
		Name:       "existing-vm",
		VNCEnabled: false,
		VNCPort:    requestedPort,
	}).Error; err != nil {
		t.Fatalf("seed configured VNC port: %v", err)
	}

	resolvedPort, reassigned, err := service.resolveMigratedVMVNCPort(107, requestedPort)
	if err != nil {
		t.Fatalf("resolve configured VNC port: %v", err)
	}
	if !reassigned || resolvedPort == requestedPort {
		t.Fatalf(
			"configured port resolution = port %d reassigned %v, want a different port",
			resolvedPort,
			reassigned,
		)
	}
}

func TestResolveMigratedVMVNCPortPreservesOwnRetryPort(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{})
	service := &Service{DB: db}
	requestedPort, listener := listenOnEphemeralTCPPort(t)
	if err := listener.Close(); err != nil {
		t.Fatalf("release requested VNC port: %v", err)
	}
	if err := db.Create(&vmModels.VM{
		RID:        107,
		Name:       "partially-imported-vm",
		VNCEnabled: true,
		VNCPort:    requestedPort,
	}).Error; err != nil {
		t.Fatalf("seed retry VM record: %v", err)
	}

	resolvedPort, reassigned, err := service.resolveMigratedVMVNCPort(107, requestedPort)
	if err != nil {
		t.Fatalf("resolve retry VNC port: %v", err)
	}
	if reassigned || resolvedPort != requestedPort {
		t.Fatalf(
			"retry port resolution = port %d reassigned %v, want unchanged %d",
			resolvedPort,
			reassigned,
			requestedPort,
		)
	}
}

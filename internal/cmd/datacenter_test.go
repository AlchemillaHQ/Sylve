// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
)

func TestDatacenterClusterAddressCommandsSendGuardedTypedOperations(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantOperation string
		validate      func(*testing.T, json.RawMessage)
	}{
		{
			name:          "readdress",
			args:          []string{"datacenter", "cluster", "readdress", "--new-ip", "192.0.2.20", "--allow-disruption"},
			wantOperation: consoleprotocol.OperationDatacenterClusterReaddress,
			validate: func(t *testing.T, payload json.RawMessage) {
				var request consoleprotocol.DatacenterClusterReaddressPayload
				if err := json.Unmarshal(payload, &request); err != nil {
					t.Fatal(err)
				}
				if request.NewIP != "192.0.2.20" || !request.AllowDisruption {
					t.Fatalf("payload = %+v", request)
				}
			},
		},
		{
			name: "repair",
			args: []string{
				"datacenter", "cluster", "repair-address", "--node-id", "node-2",
				"--new-ip", "192.0.2.20", "--allow-disruption",
			},
			wantOperation: consoleprotocol.OperationDatacenterClusterRepairAddress,
			validate: func(t *testing.T, payload json.RawMessage) {
				var request consoleprotocol.DatacenterClusterRepairAddressPayload
				if err := json.Unmarshal(payload, &request); err != nil {
					t.Fatal(err)
				}
				if request.NodeID != "node-2" || request.NewIP != "192.0.2.20" || !request.AllowDisruption {
					t.Fatalf("payload = %+v", request)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SYLVE_DATA_PATH", "")
			configDir, err := os.MkdirTemp("/tmp", "dc")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(configDir) })
			dataPath := filepath.Join(configDir, "data")
			configPath := filepath.Join(configDir, "config.json")
			if err := os.WriteFile(configPath, []byte(`{"dataPath":"`+dataPath+`"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(consoleprotocol.SocketPath(dataPath)), 0o700); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", consoleprotocol.SocketPath(dataPath))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()

			requests := make(chan consoleprotocol.Request, 1)
			go func() {
				connection, err := listener.Accept()
				if err != nil {
					return
				}
				defer connection.Close()
				var request consoleprotocol.Request
				if json.NewDecoder(connection).Decode(&request) == nil {
					requests <- request
					_ = json.NewEncoder(connection).Encode(consoleprotocol.Response{Output: "ok\n"})
				}
			}()

			args := append([]string{"sylve", "--config", configPath}, test.args...)
			if err := newRootCommand(nil, func() bool { return true }).Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			request := <-requests
			if request.Operation != test.wantOperation {
				t.Fatalf("operation = %q", request.Operation)
			}
			test.validate(t, request.Payload)
		})
	}
}

func TestDatacenterClusterAddressCommandsRequireDisruptionAcknowledgement(t *testing.T) {
	tests := [][]string{
		{"sylve", "datacenter", "cluster", "readdress", "--new-ip", "192.0.2.20"},
		{"sylve", "datacenter", "cluster", "repair-address", "--node-id", "node-2", "--new-ip", "192.0.2.20"},
		{"sylve", "datacenter", "cluster", "recover-ip", "--new-ip", "192.0.2.20"},
	}
	for _, args := range tests {
		if err := newRootCommand(nil, func() bool { return true }).Run(context.Background(), args); err == nil {
			t.Fatalf("expected --allow-disruption to be required for %q", args[3])
		}
	}
}

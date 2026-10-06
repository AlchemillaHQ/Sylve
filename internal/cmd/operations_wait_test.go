// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	"github.com/urfave/cli/v3"
)

func TestCLIWaitService(t *testing.T) {
	testCases := []struct {
		name      string
		args      []string
		operation string
	}{
		{"after", []string{"switches", "list", "--wait-service", "1", "--json"}, consoleprotocol.OperationSwitchList},
		{"before", []string{"--wait-service", "1", "switches", "list", "--json"}, consoleprotocol.OperationSwitchList},
		{"nested", []string{"datacenter", "cluster", "status", "--wait-service", "1", "--json"}, consoleprotocol.OperationDatacenterClusterStatus},
		{"serial", []string{"vms", "access", "serial", "--rid", "100", "--wait-service", "1", "--json"}, consoleprotocol.OperationVMAccessSerial},
		{"bootstrap", []string{"jails", "bootstrap", "create", "--pool", "zroot", "--version", "15.0", "--type", "base", "--wait", "--wait-service", "1", "--json"}, consoleprotocol.OperationBootstrapCreate},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			configPath, socketPath := serviceWaitConfig(t)
			root := newRootCommand(nil, func() bool { return true })
			args := append([]string{"sylve", "--config", configPath}, testCase.args...)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var runErr error
			var requests <-chan consoleprotocol.Request
			var serverErr <-chan error
			out := captureStdout(t, func() {
				done := make(chan error, 1)
				go func() { done <- root.Run(ctx, args) }()
				time.Sleep(50 * time.Millisecond)
				requests, serverErr = serveCLIServiceWait(t, socketPath)
				runErr = <-done
			})
			if runErr != nil || out != "[]\n" {
				t.Fatalf("stdout = %q, error = %v, want only daemon output", out, runErr)
			}
			if err := <-serverErr; err != nil {
				t.Fatalf("serve response: %v", err)
			}
			request := <-requests
			if request.Operation != testCase.operation {
				t.Fatalf("operation = %q, want %q", request.Operation, testCase.operation)
			}
			var payload struct {
				JSON bool `json:"json"`
				Wait bool `json:"wait"`
			}
			if err := json.Unmarshal(request.Payload, &payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if !payload.JSON || (testCase.name == "bootstrap" && !payload.Wait) {
				t.Fatalf("JSON or existing bootstrap --wait flag lost: %+v", payload)
			}
		})
	}
}

func TestCLIWaitServiceErrors(t *testing.T) {
	testCases := []struct {
		name string
		wait []string
		want string
	}{
		{"default", nil, "sylve daemon is not running"},
		{"zero", []string{"--wait-service", "0"}, "sylve daemon is not running"},
		{"timeout", []string{"--wait-service", "1"}, "did not become available within 1s"},
		{"config", []string{"--wait-service", "60"}, "config file not found"},
		{"malformed", []string{"--wait-service", "60"}, "decode config"},
		{"canceled", []string{"--wait-service", "60"}, "context canceled"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			configPath, _ := serviceWaitConfig(t)
			switch testCase.name {
			case "config":
				configPath = filepath.Join(t.TempDir(), "missing.json")
			case "malformed":
				if err := os.WriteFile(configPath, []byte("{"), 0o600); err != nil {
					t.Fatalf("write malformed config: %v", err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if testCase.name == "canceled" {
				timer := time.AfterFunc(40*time.Millisecond, cancel)
				defer timer.Stop()
			}
			root := newRootCommand(nil, func() bool { return true })
			args := append([]string{"sylve", "--config", configPath, "switches", "list", "--json"}, testCase.wait...)
			var runErr error
			out := captureStdout(t, func() { runErr = root.Run(ctx, args) })
			if runErr == nil || !strings.Contains(runErr.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", runErr, testCase.want)
			}
			if testCase.name == "canceled" && !errors.Is(runErr, context.Canceled) {
				t.Fatalf("error = %v, want wrapped cancellation", runErr)
			}
			var output map[string]string
			if err := json.Unmarshal([]byte(out), &output); err != nil {
				t.Fatalf("stdout must contain only JSON: %q: %v", out, err)
			}
			if len(output) != 1 || output["error"] != runErr.Error() {
				t.Fatalf("stdout = %q, want a single JSON error", out)
			}
		})
	}
}

func TestCLIWaitServiceValidation(t *testing.T) {
	for _, value := range []string{"0", "60", "-1", "9223372037", "0.5", "60s"} {
		t.Run(value, func(t *testing.T) {
			called := false
			root := newRootCommand(func(ctx context.Context, command *cli.Command) error {
				called = true
				return nil
			}, func() bool { return true })
			root.Command("switches").Command("list").Action = root.Action
			root.Writer = &bytes.Buffer{}
			root.ErrWriter = &bytes.Buffer{}
			err := root.Run(context.Background(), []string{"sylve", "switches", "list", "--wait-service", value})
			valid := value == "0" || value == "60"
			if valid && (err != nil || !called) {
				t.Fatalf("valid wait %q: called = %v, error = %v", value, called, err)
			}
			if !valid && (err == nil || called) {
				t.Fatalf("invalid wait %q: called = %v, error = %v", value, called, err)
			}
		})
	}
}

func serviceWaitConfig(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("SYLVE_DATA_PATH", "")
	rootDir := t.TempDir()
	dataPath := filepath.Join(rootDir, "data")
	configPath := filepath.Join(rootDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"dataPath":"`+dataPath+`"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath, consoleprotocol.SocketPath(dataPath)
}

func serveCLIServiceWait(t *testing.T, socketPath string) (<-chan consoleprotocol.Request, <-chan error) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	if err := listener.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set accept deadline: %v", err)
	}
	requests := make(chan consoleprotocol.Request, 1)
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			serverErr <- err
			return
		}
		var request consoleprotocol.Request
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			serverErr <- err
			return
		}
		requests <- request
		serverErr <- json.NewEncoder(conn).Encode(consoleprotocol.Response{Output: "[]\n"})
	}()
	return requests, serverErr
}

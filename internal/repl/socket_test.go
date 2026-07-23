// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
)

func TestProcessSocketRequestCommandRequired(t *testing.T) {
	resp := processSocketRequest(&Context{}, socketRequest{Command: "   "})
	if resp.Error != "command_required" {
		t.Fatalf("expected command_required, got %q", resp.Error)
	}
	if resp.Output != "" {
		t.Fatalf("expected empty output, got %q", resp.Output)
	}
}

func TestProcessSocketRequestExecutesCommand(t *testing.T) {
	resp := processSocketRequest(&Context{}, socketRequest{Command: "ping"})
	if resp.Error != "" {
		t.Fatalf("expected no error, got %q", resp.Error)
	}
	if strings.TrimSpace(resp.Output) != "pong" {
		t.Fatalf("expected pong output, got %q", resp.Output)
	}
	if resp.Close {
		t.Fatalf("expected ping to keep session open")
	}
}

func TestProcessSocketRequestRejectsUnknownOperation(t *testing.T) {
	resp := processSocketRequest(&Context{}, socketRequest{Operation: "unknown"})
	if resp.Error != "unknown_operation" {
		t.Fatalf("expected unknown_operation, got %q", resp.Error)
	}
}

func TestProcessSocketRequestCreateRequiresPayloadAndService(t *testing.T) {
	resp := processSocketRequest(&Context{}, socketRequest{Operation: consoleprotocol.OperationJailCreate})
	if resp.Error != "invalid_jail_create_request: payload_required" {
		t.Fatalf("expected missing payload error, got %q", resp.Error)
	}

	payload, err := json.Marshal(consoleprotocol.JailCreatePayload{})
	if err != nil {
		t.Fatalf("marshal jail create payload: %v", err)
	}
	resp = processSocketRequest(&Context{}, socketRequest{
		Operation: consoleprotocol.OperationJailCreate,
		Payload:   payload,
	})
	if resp.Error != "jail_service_unavailable" {
		t.Fatalf("expected jail_service_unavailable, got %q", resp.Error)
	}
}

func TestProcessSocketRequestRejectsUnknownPayloadFields(t *testing.T) {
	resp := processSocketRequest(&Context{}, socketRequest{
		Operation: consoleprotocol.OperationJailList,
		Payload:   json.RawMessage(`{"unexpected": true}`),
	})
	if !strings.Contains(resp.Error, "unknown field \"unexpected\"") {
		t.Fatalf("expected unknown payload field error, got %q", resp.Error)
	}
}

func TestProcessSocketRequestOperationsRequirePayload(t *testing.T) {
	testCases := []struct {
		operation string
		wantError string
	}{
		{consoleprotocol.OperationVMList, "invalid_vm_list_request: payload_required"},
		{consoleprotocol.OperationVMGet, "invalid_vm_get_request: payload_required"},
		{consoleprotocol.OperationVMCreate, "invalid_vm_create_request: payload_required"},
		{consoleprotocol.OperationVMAction, "invalid_vm_action_request: payload_required"},
		{consoleprotocol.OperationVMDelete, "invalid_vm_delete_request: payload_required"},
		{consoleprotocol.OperationVMPurge, "invalid_vm_purge_request: payload_required"},
		{consoleprotocol.OperationVMNetworks, "invalid_vm_networks_request: payload_required"},
		{consoleprotocol.OperationVMNetworkAttach, "invalid_vm_network_attach_request: payload_required"},
		{consoleprotocol.OperationVMNetworkDetach, "invalid_vm_network_detach_request: payload_required"},
		{consoleprotocol.OperationVMQGASend, "invalid_vm_qga_request: payload_required"},
		{consoleprotocol.OperationSwitchList, "invalid_switch_list_request: payload_required"},
		{consoleprotocol.OperationSwitchCreate, "invalid_switch_create_request: payload_required"},
		{consoleprotocol.OperationSwitchDelete, "invalid_switch_delete_request: payload_required"},
		{consoleprotocol.OperationSwitchEdit, "invalid_switch_edit_request: payload_required"},
		{consoleprotocol.OperationObjectList, "invalid_object_list_request: payload_required"},
		{consoleprotocol.OperationObjectCreate, "invalid_object_create_request: payload_required"},
		{consoleprotocol.OperationObjectEdit, "invalid_object_edit_request: payload_required"},
		{consoleprotocol.OperationObjectDelete, "invalid_object_delete_request: payload_required"},
		{consoleprotocol.OperationDownloadList, "invalid_download_list_request: payload_required"},
		{consoleprotocol.OperationDownloadStart, "invalid_download_start_request: payload_required"},
		{consoleprotocol.OperationDownloadDelete, "invalid_download_delete_request: payload_required"},
		{consoleprotocol.OperationTaskListActive, "invalid_task_active_request: payload_required"},
		{consoleprotocol.OperationTaskListRecent, "invalid_task_recent_request: payload_required"},
		{consoleprotocol.OperationTaskGet, "invalid_task_get_request: payload_required"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.operation, func(t *testing.T) {
			resp := processSocketRequest(&Context{}, socketRequest{Operation: testCase.operation})
			if resp.Error != testCase.wantError {
				t.Fatalf("error = %q, want %q", resp.Error, testCase.wantError)
			}
		})
	}
}

func TestProcessSocketRequestSwitchCreateUsesNetworkService(t *testing.T) {
	payload, err := json.Marshal(consoleprotocol.SwitchCreatePayload{
		Type: "standard",
		Standard: &consoleprotocol.StandardSwitchCreateRequest{
			Name: "isolated",
		},
	})
	if err != nil {
		t.Fatalf("marshal switch create payload: %v", err)
	}

	resp := processSocketRequest(&Context{}, socketRequest{
		Operation: consoleprotocol.OperationSwitchCreate,
		Payload:   payload,
	})
	if resp.Error != "network_service_unavailable" {
		t.Fatalf("expected network_service_unavailable, got %q", resp.Error)
	}
}

func TestProcessSocketRequestObjectCreateUsesNetworkService(t *testing.T) {
	payload, err := json.Marshal(consoleprotocol.ObjectCreatePayload{
		Request: consoleprotocol.NetworkObjectRequest{
			Name:   "lan4",
			Type:   "network",
			Values: []string{"192.0.2.0/24"},
		},
	})
	if err != nil {
		t.Fatalf("marshal object create payload: %v", err)
	}

	resp := processSocketRequest(&Context{}, socketRequest{
		Operation: consoleprotocol.OperationObjectCreate,
		Payload:   payload,
	})
	if resp.Error != "network_service_unavailable" {
		t.Fatalf("expected network_service_unavailable, got %q", resp.Error)
	}
}

func TestProcessSocketRequestShutdownTriggersSignal(t *testing.T) {
	signals := make(chan os.Signal, 1)
	ctx := &Context{QuitChan: signals}

	resp := processSocketRequest(ctx, socketRequest{Command: "shutdown"})
	if resp.Error != "" {
		t.Fatalf("expected no error, got %q", resp.Error)
	}
	if !resp.Close {
		t.Fatalf("expected shutdown to close session")
	}

	select {
	case got := <-signals:
		if got != syscall.SIGTERM {
			t.Fatalf("expected SIGTERM, got %v", got)
		}
	default:
		t.Fatalf("expected shutdown signal")
	}
}

func TestHandleSocketConnMalformedRequest(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleSocketConn(&Context{}, serverConn)
	}()

	if _, err := clientConn.Write([]byte("[]\n")); err != nil {
		t.Fatalf("failed writing malformed request: %v", err)
	}

	var resp socketResponse
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("failed decoding response: %v", err)
	}
	if resp.Error != "invalid_request" {
		t.Fatalf("expected invalid_request, got %q", resp.Error)
	}

	<-done
}

func TestStartSocketServerPermissionsAndCleanup(t *testing.T) {
	dataPath := t.TempDir()
	socketPath := consoleprotocol.SocketPath(dataPath)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("create socket directory: %v", err)
	}

	stale, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed creating stale socket: %v", err)
	}
	_ = stale.Close()

	server, err := startSocketServer(&Context{}, socketPath)
	if err != nil {
		t.Fatalf("startSocketServer failed: %v", err)
	}

	info, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatalf("expected socket path to exist: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("expected socket file, got mode %v", info.Mode())
	}
	if perms := info.Mode().Perm(); perms != 0600 {
		t.Fatalf("expected permissions 0600, got %o", perms)
	}

	directory, err := os.Stat(filepath.Dir(socketPath))
	if err != nil {
		t.Fatalf("expected socket directory: %v", err)
	}
	if perms := directory.Mode().Perm(); perms != 0o700 {
		t.Fatalf("expected directory permissions 0700, got %o", perms)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("server close failed: %v", err)
	}
	if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("expected socket to be removed, got err=%v", err)
	}
}

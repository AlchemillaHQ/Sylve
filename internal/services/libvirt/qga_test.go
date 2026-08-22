// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/digitalocean/go-libvirt"
)

func TestQGACallRawSuccess(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()

		dec := json.NewDecoder(server)
		enc := json.NewEncoder(server)

		var req qgaRequest
		if err := dec.Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			return
		}
		if req.Execute != "guest-info" {
			t.Errorf("unexpected command: %s", req.Execute)
			return
		}

		if err := enc.Encode(qgaResponse{
			Return: json.RawMessage(`{"version":"8.2.0"}`),
		}); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}()

	enc := json.NewEncoder(client)
	dec := json.NewDecoder(client)

	resp, err := qgaCallRaw(client, enc, dec, "guest-info", nil)
	if err != nil {
		t.Fatalf("qgaCallRaw returned error: %v", err)
	}
	if string(resp) != `{"version":"8.2.0"}` {
		t.Fatalf("unexpected response: %s", string(resp))
	}

	<-done
}

func TestParseQGAGuestInfoReturnsSortedCapabilities(t *testing.T) {
	version, capabilities, err := parseQGAGuestInfo(json.RawMessage(`{
		"version":"9.2.1",
		"supported_commands":[
			{"name":"guest-sync","enabled":true,"success-response":true},
			{"name":"guest-info","enabled":false,"success-response":false}
		]
	}`))
	if err != nil {
		t.Fatalf("parse guest info: %v", err)
	}
	if version != "9.2.1" || len(capabilities) != 2 || capabilities[0].Name != "guest-info" ||
		capabilities[1].Name != "guest-sync" || !capabilities[1].SuccessResponse {
		t.Fatalf("version=%q capabilities=%#v", version, capabilities)
	}
}

func TestInspectQemuGuestAgentDisabledIsSuccessful(t *testing.T) {
	db := newVMDeleteTestDB(t)
	vm := vmModels.VM{RID: 750, Name: "qga-disabled", QemuGuestAgent: false}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	status, err := (&Service{DB: db}).InspectQemuGuestAgent(vm.RID)
	if err != nil {
		t.Fatalf("inspect disabled QGA: %v", err)
	}
	if status.RID != vm.RID || status.Enabled || status.Reachable || status.DomainState != "unknown" ||
		status.UnavailableReason != "qemu_guest_agent_disabled" || status.Capabilities == nil {
		t.Fatalf("status = %#v", status)
	}
}

func TestQGACallRawPropagatesAgentError(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()

		dec := json.NewDecoder(server)
		enc := json.NewEncoder(server)

		var req qgaRequest
		if err := dec.Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			return
		}

		if err := enc.Encode(qgaResponse{
			Error: &qgaError{
				Class: "CommandNotFound",
				Desc:  "unknown command",
			},
		}); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}()

	enc := json.NewEncoder(client)
	dec := json.NewDecoder(client)

	_, err := qgaCallRaw(client, enc, dec, "guest-bogus", nil)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "qga_error_CommandNotFound") {
		t.Fatalf("unexpected error: %v", err)
	}

	<-done
}

func TestQGACallRawSendsArguments(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()

		dec := json.NewDecoder(server)
		enc := json.NewEncoder(server)

		var req qgaRequest
		if err := dec.Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			return
		}

		args, ok := req.Arguments.(map[string]any)
		if !ok {
			t.Errorf("expected arguments object, got %T", req.Arguments)
			return
		}
		if path, ok := args["path"].(string); !ok || path != "/bin/ls" {
			t.Errorf("unexpected arguments payload: %#v", req.Arguments)
			return
		}

		if err := enc.Encode(qgaResponse{
			Return: json.RawMessage(`{"pid":42}`),
		}); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}()

	enc := json.NewEncoder(client)
	dec := json.NewDecoder(client)

	_, err := qgaCallRaw(client, enc, dec, "guest-exec", map[string]any{"path": "/bin/ls"})
	if err != nil {
		t.Fatalf("qgaCallRaw returned error: %v", err)
	}

	<-done
}

func TestDecodeQGAResponse(t *testing.T) {
	response, err := decodeQGAResponse([]byte(`{"return":{"version":"9.0.0"}}`))
	if err != nil {
		t.Fatalf("decodeQGAResponse returned error: %v", err)
	}
	if string(response) != `{"version":"9.0.0"}` {
		t.Fatalf("unexpected response: %s", response)
	}
}

func TestDecodeQGAResponsePropagatesAgentError(t *testing.T) {
	_, err := decodeQGAResponse([]byte(`{"error":{"class":"CommandNotFound","desc":"unknown command"}}`))
	if err == nil || !strings.Contains(err.Error(), "qga_error_CommandNotFound") {
		t.Fatalf("expected QGA protocol error, got: %v", err)
	}
}

func TestDecodeQGAResponseRejectsMissingReturnAndAcceptsExplicitNull(t *testing.T) {
	if _, err := decodeQGAResponse([]byte(`{}`)); err == nil {
		t.Fatal("expected response without return or error to fail")
	}

	response, err := decodeQGAResponse([]byte(`{"return":null}`))
	if err != nil {
		t.Fatalf("expected explicit null return to succeed: %v", err)
	}
	if string(response) != "null" {
		t.Fatalf("unexpected explicit null response: %s", response)
	}
}

func TestIsLibvirtErrorNumberRejectsOtherErrors(t *testing.T) {
	err := libvirt.Error{Code: uint32(libvirt.ErrArgumentUnsupported), Message: "agent is not configured"}
	if !isLibvirtErrorNumber(err, libvirt.ErrArgumentUnsupported) {
		t.Fatalf("expected matching libvirt error number: %v", err)
	}
	if isLibvirtErrorNumber(err, libvirt.ErrAgentCommandFailed) {
		t.Fatalf("did not expect different libvirt error number to match: %v", err)
	}
}

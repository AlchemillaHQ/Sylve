// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import (
	"encoding/json"
	"net"
	"path/filepath"
	"reflect"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
)

func TestStandardSwitchServiceRequestMapsEveryField(t *testing.T) {
	accessVLAN, hostVLAN := 20, 30
	policy := bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN}
	request := StandardSwitchCreateRequest{
		Name: "switch-mapping", MTU: 9000, VLAN: 12,
		Network4: 1, Gateway4: 2, Network6: 3, Gateway6: 4,
		Network4Manual: "192.0.2.1/24", Gateway4Manual: "192.0.2.254",
		Network6Manual: "2001:db8::1/64", Gateway6Manual: "2001:db8::fe",
		DisableIPv6: true, SLAAC: true, Private: true, DefaultRoute: true, DefaultRoute6: true,
		DisableBridgeOffloads: true, DHCP: true, Ports: []string{"em0", "em1"},
		BridgeMAC:     StandardSwitchMACSourceRequest{Mode: "object", MACObjectID: 7},
		VLANFiltering: true, DefaultAccessVLAN: &accessVLAN, HostVLAN: &hostVLAN,
		PortPolicies: map[string]bridgevlan.PortPolicy{"em0": policy},
	}
	mapped := StandardSwitchServiceRequest(request)
	if mapped.Name != request.Name {
		t.Fatalf("name = %q, want %q", mapped.Name, request.Name)
	}
	config := mapped.StandardSwitchConfig
	if config.MTU != request.MTU || config.VLAN != request.VLAN {
		t.Fatalf("mtu/vlan = %d/%d, want %d/%d", config.MTU, config.VLAN, request.MTU, request.VLAN)
	}
	if config.Network4ID != request.Network4 || config.Gateway4ID != request.Gateway4 ||
		config.Network6ID != request.Network6 || config.Gateway6ID != request.Gateway6 {
		t.Fatalf("object IDs not mapped: %#v", config)
	}
	manual := config.Manual
	if manual.Network4 != request.Network4Manual || manual.Gateway4 != request.Gateway4Manual ||
		manual.Network6 != request.Network6Manual || manual.Gateway6 != request.Gateway6Manual {
		t.Fatalf("manual addresses not mapped: %#v", manual)
	}
	if !config.DisableIPv6 || !config.SLAAC || !config.Private || !config.DefaultRoute ||
		!config.DefaultRoute6 || !config.DisableBridgeOffloads || !config.DHCP {
		t.Fatalf("boolean fields not mapped: %#v", config)
	}
	if config.MACSource.Mode != "object" || config.MACSource.MACObjectID != 7 || config.MACSource.Port != "" {
		t.Fatalf("bridge MAC source not mapped: %#v", config.MACSource)
	}
	if !reflect.DeepEqual(config.Ports, request.Ports) {
		t.Fatalf("ports not mapped: %#v", config.Ports)
	}
	if !config.VLANConfig.Filtering ||
		config.VLANConfig.DefaultAccessVLAN == nil || *config.VLANConfig.DefaultAccessVLAN != accessVLAN ||
		config.VLANConfig.HostVLAN == nil || *config.VLANConfig.HostVLAN != hostVLAN {
		t.Fatalf("VLAN config not mapped: %#v", config.VLANConfig)
	}
	if !reflect.DeepEqual(config.VLANConfig.PortPolicies, request.PortPolicies) {
		t.Fatalf("port policies not mapped: %#v", config.VLANConfig.PortPolicies)
	}
}

func TestExecuteOperationResponsePreservesOutputOnError(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "console.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()

		var request Request
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			serverErr <- err
			return
		}
		serverErr <- json.NewEncoder(conn).Encode(Response{
			Output: `{"archived":false}` + "\n",
			Error:  "bootstrap_apply_failed",
		})
	}()

	response, err := ExecuteOperationResponse(socketPath, OperationBootstrapApply, BootstrapApplyPayload{JSON: true})
	if err == nil {
		t.Fatal("expected protocol error to be surfaced")
	}
	if response.Output != `{"archived":false}`+"\n" {
		t.Fatalf("output lost on protocol error: %q", response.Output)
	}
	if response.Error != "bootstrap_apply_failed" {
		t.Fatalf("response error = %q, want bootstrap_apply_failed", response.Error)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("serve response: %v", err)
	}
}

func TestExecuteOperationSendsTypedPayload(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "console.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	received := make(chan Request, 1)
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()

		var request Request
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			serverErr <- err
			return
		}
		received <- request
		serverErr <- json.NewEncoder(conn).Encode(Response{Output: "created\n"})
	}()

	ctid := uint(101)
	request := jailServiceInterfaces.CreateJailRequest{
		Name:          "HAOS-W",
		CTID:          &ctid,
		Pool:          "zroot",
		BootstrapName: "14.2-RELEASE",
		SwitchName:    "none",
		Type:          jailModels.JailTypeFreeBSD,
		Description:   "contains spaces safely",
	}

	output, err := executeOperation(socketPath, OperationJailCreate, JailCreatePayload{
		Request: request,
		JSON:    true,
	})
	if err != nil {
		t.Fatalf("execute operation: %v", err)
	}
	if output != "created\n" {
		t.Fatalf("output = %q, want created response", output)
	}

	envelope := <-received
	if envelope.Operation != OperationJailCreate || envelope.Command != "" {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}

	var payload JailCreatePayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Request.CTID == nil || *payload.Request.CTID != ctid {
		t.Fatalf("CTID = %v, want %d", payload.Request.CTID, ctid)
	}
	if payload.Request.Description != request.Description || !payload.JSON {
		t.Fatalf("decoded payload = %#v", payload)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("serve response: %v", err)
	}
}

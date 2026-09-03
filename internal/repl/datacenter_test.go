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
	"strings"
	"testing"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
)

func TestParseClusterAddressFlagsRequiresAcknowledgement(t *testing.T) {
	newIP, nodeID, allow, err := parseClusterAddressFlags([]string{
		"--node-id", "node-2", "--new-ip", "192.0.2.20", "--allow-disruption",
	}, true)
	if err != nil || newIP != "192.0.2.20" || nodeID != "node-2" || !allow {
		t.Fatalf("newIP=%q nodeID=%q allow=%v err=%v", newIP, nodeID, allow, err)
	}
	if _, _, _, err := parseClusterAddressFlags([]string{"--new-ip", "192.0.2.20"}, false); err == nil {
		t.Fatal("expected missing acknowledgement error")
	}
}

func TestParseGuestIdentityReclaimFlags(t *testing.T) {
	guestID, force, confirmation, err := parseGuestIdentityReclaimFlags([]string{"--id", "505"})
	if err != nil || guestID != 505 || force || confirmation != "" {
		t.Fatalf("guestID=%d force=%v confirmation=%q err=%v", guestID, force, confirmation, err)
	}
	guestID, force, confirmation, err = parseGuestIdentityReclaimFlags([]string{
		"--force", "--confirm", "506", "--id", "506",
	})
	if err != nil || guestID != 506 || !force || confirmation != "506" {
		t.Fatalf("guestID=%d force=%v confirmation=%q err=%v", guestID, force, confirmation, err)
	}
	invalid := [][]string{
		{},
		{"--id", "0"},
		{"--id", "10000"},
		{"--id", "505", "--force"},
		{"--id", "505", "--force", "--confirm", "506"},
		{"--id", "505", "--confirm", "505"},
	}
	for _, args := range invalid {
		if _, _, _, err := parseGuestIdentityReclaimFlags(args); err == nil {
			t.Fatalf("expected validation error for %v", args)
		}
	}
}

func TestDatacenterClusterCommandsUseCorrectMutationAdmission(t *testing.T) {
	if rawCommandRequiresMutation([]string{"datacenter", "cluster", "guest-ids", "list"}) {
		t.Fatal("guest ID list must not use mutation admission")
	}
	if !rawCommandRequiresMutation([]string{"datacenter", "cluster", "guest-ids", "reclaim", "--id", "505"}) {
		t.Fatal("guest ID reclaim must use ordinary mutation admission")
	}
	if typedSocketOperationRequiresMutation(consoleprotocol.OperationDatacenterClusterGuestIDsList) {
		t.Fatal("typed guest ID list must not use mutation admission")
	}
	if !typedSocketOperationRequiresMutation(consoleprotocol.OperationDatacenterClusterGuestIDReclaim) {
		t.Fatal("typed guest ID reclaim must use ordinary mutation admission")
	}
	if rawCommandRequiresMutation([]string{"datacenter", "cluster", "readdress", "--new-ip", "192.0.2.20", "--allow-disruption"}) {
		t.Fatal("readdress must drain its own mutation gate")
	}
	if !rawCommandRequiresMutation([]string{"datacenter", "cluster", "repair-address", "--node-id", "node-2", "--new-ip", "192.0.2.20", "--allow-disruption"}) {
		t.Fatal("peer repair must use ordinary mutation admission")
	}
	if typedSocketOperationRequiresMutation(consoleprotocol.OperationDatacenterClusterReaddress) {
		t.Fatal("typed readdress must drain its own mutation gate")
	}
	if !typedSocketOperationRequiresMutation(consoleprotocol.OperationDatacenterClusterRepairAddress) {
		t.Fatal("typed peer repair must use ordinary mutation admission")
	}
}

func TestGuestIdentityReclaimSocketRejectsUnsafeRequests(t *testing.T) {
	tests := []consoleprotocol.DatacenterClusterGuestIDReclaimPayload{
		{},
		{GuestID: 10000},
		{GuestID: 505, Force: true},
		{GuestID: 505, Force: true, Confirmation: "506"},
		{GuestID: 505, Confirmation: "505"},
	}
	for _, request := range tests {
		payload, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		response := processDatacenterClusterGuestIDReclaimSocketRequest(nil, payload)
		if !strings.Contains(response.Error, "invalid_datacenter_cluster_guest_id_reclaim_request") {
			t.Fatalf("request=%+v error=%q", request, response.Error)
		}
	}
}

func TestGuestIdentityClaimConsoleShapeOmitsToken(t *testing.T) {
	claims := []consoleprotocol.DatacenterClusterGuestIdentityClaim{{
		GuestID: 505, GuestKind: "jail", OwnerNodeID: "node-2",
	}}
	encoded, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "token") {
		t.Fatalf("claim JSON exposes token: %s", encoded)
	}
	formatted := formatDatacenterClusterGuestIDClaims(claims)
	for _, expected := range []string{"505", "jail", "node-2"} {
		if !strings.Contains(formatted, expected) {
			t.Fatalf("formatted claims %q missing %q", formatted, expected)
		}
	}
}

func TestAddressSocketOperationsRejectMissingAcknowledgement(t *testing.T) {
	service := clusterService.NewClusterService(nil, nil, nil).(*clusterService.Service)
	if err := service.ReopenMutations(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		operation string
		payload   any
	}{
		{consoleprotocol.OperationDatacenterClusterReaddress, consoleprotocol.DatacenterClusterReaddressPayload{NewIP: "192.0.2.20"}},
		{consoleprotocol.OperationDatacenterClusterRepairAddress, consoleprotocol.DatacenterClusterRepairAddressPayload{NodeID: "node-2", NewIP: "192.0.2.20"}},
	}
	for _, test := range tests {
		payload, err := json.Marshal(test.payload)
		if err != nil {
			t.Fatal(err)
		}
		response := processSocketRequest(&Context{Cluster: service}, socketRequest{Operation: test.operation, Payload: payload})
		if !strings.Contains(response.Error, "cluster_readdress_disruption_acknowledgement_required") {
			t.Fatalf("operation %s error = %q", test.operation, response.Error)
		}
	}
}

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

func TestReaddressCommandsUseCorrectMutationAdmission(t *testing.T) {
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

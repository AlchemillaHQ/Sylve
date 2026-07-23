// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

func TestExecuteLineVmsQGASendSyntax(t *testing.T) {
	testCases := []string{
		"vms qga send 101 guest-info",
		"vms qga 101 guest-get-osinfo",
		"vms 101 qga send guest-network-get-interfaces",
		"vms 101 qga guest-info",
	}

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			var out bytes.Buffer
			ctx := &Context{Out: &out}

			shouldContinue := ExecuteLine(ctx, tc)
			if !shouldContinue {
				t.Fatalf("expected command to keep session running")
			}

			if !strings.Contains(out.String(), "Error: VM service unavailable.") {
				t.Fatalf("expected VM service error, got %q", out.String())
			}
		})
	}
}

func TestHandleVmsListRejectsTrailingArguments(t *testing.T) {
	var out bytes.Buffer
	handleVms(&Context{Out: &out}, []string{"list", "extra"})

	if !strings.Contains(out.String(), "Usage: vms list") {
		t.Fatalf("unexpected list output: %q", out.String())
	}
}

func TestParseVMDeleteArgs(t *testing.T) {
	rid, deleteMACs, deleteRawDisks, deleteVolumes, err := parseVMDeleteArgs([]string{
		"701", "--delete-macs", "--delete-raw-disks", "--delete-volumes",
	})
	if err != nil {
		t.Fatalf("parse VM delete arguments: %v", err)
	}
	if rid != 701 || !deleteMACs || !deleteRawDisks || !deleteVolumes {
		t.Fatalf("parsed delete arguments = %d, %t, %t, %t", rid, deleteMACs, deleteRawDisks, deleteVolumes)
	}

	if _, _, _, _, err := parseVMDeleteArgs([]string{"701", "--unknown"}); err == nil || !strings.Contains(err.Error(), "Usage: vms delete") {
		t.Fatalf("invalid delete arguments error = %v", err)
	}
}

func TestParseVMPurgeArgs(t *testing.T) {
	rid, deleteMACs, err := parseVMPurgeArgs([]string{"702", "--delete-macs"})
	if err != nil {
		t.Fatalf("parse VM purge arguments: %v", err)
	}
	if rid != 702 || !deleteMACs {
		t.Fatalf("parsed purge arguments = %d, %t", rid, deleteMACs)
	}

	if _, _, err := parseVMPurgeArgs([]string{"702", "--delete-macs", "--delete-macs"}); err == nil || !strings.Contains(err.Error(), "Usage: vms purge") {
		t.Fatalf("duplicate purge arguments error = %v", err)
	}
}

func TestBuildConsoleVMCreateRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vm.json")
	if err := os.WriteFile(path, []byte(`{"name":"vm-file","rid":703}`), 0600); err != nil {
		t.Fatalf("write VM request: %v", err)
	}

	request, err := buildConsoleVMCreateRequest([]string{"--file", path})
	if err != nil {
		t.Fatalf("build VM create request: %v", err)
	}
	if request.RID == nil || *request.RID != 703 || request.Name != "vm-file" {
		t.Fatalf("VM create request = %#v", request)
	}

	if _, err := buildConsoleVMCreateRequest([]string{path}); err == nil || !strings.Contains(err.Error(), "Usage: vms create") {
		t.Fatalf("invalid VM create arguments error = %v", err)
	}
}

func TestFormatVMListIncludesComputeResources(t *testing.T) {
	vm := vmModels.VM{
		RID:        108,
		Name:       "Alpine",
		CPUSockets: 1,
		CPUCores:   2,
		CPUThreads: 2,
		RAM:        2 * 1024 * 1024 * 1024,
		Networks:   []vmModels.Network{{}},
	}

	output := formatVMList([]vmModels.VM{vm})
	for _, want := range []string{"vCPUs", "RAM", "Alpine", "4", "2 GiB", "1"} {
		if !strings.Contains(output, want) {
			t.Fatalf("VM list missing %q:\n%s", want, output)
		}
	}

	if got := formatVMVCPUs(vmModels.VM{}); got != "-" {
		t.Fatalf("zero VM vCPUs = %q, want -", got)
	}
	if got := formatMemorySize(512 * 1024 * 1024); got != "512 MiB" {
		t.Fatalf("VM RAM = %q, want 512 MiB", got)
	}
}

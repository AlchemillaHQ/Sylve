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
	"strings"
	"testing"
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

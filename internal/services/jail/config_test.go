// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"strings"
	"testing"
)

func TestAddSylveNetworkToHookAtEndAppendsAfterUserSection(t *testing.T) {
	svc := &Service{}

	content := "#!/bin/sh\n### Start User-Managed Hook ###\necho user\n### End User-Managed Hook ###\n"
	networkContent := "ifconfig -j abcde abcde_net1b inet 192.168.1.10/24"

	out := svc.AddSylveNetworkToHookAtEnd(content, networkContent)

	userEndIdx := strings.Index(out, "### End User-Managed Hook ###")
	managedStartIdx := strings.Index(out, "### Start Sylve-Managed Network ###")
	if userEndIdx == -1 || managedStartIdx == -1 {
		t.Fatalf("expected both user and managed markers in output, got:\n%s", out)
	}

	if managedStartIdx <= userEndIdx {
		t.Fatalf("expected managed section to be appended after user section, got:\n%s", out)
	}
}

func TestAddSylveNetworkToHookAtEndIsIdempotent(t *testing.T) {
	svc := &Service{}

	content := "#!/bin/sh\n### Start User-Managed Hook ###\necho user\n### End User-Managed Hook ###\n"

	first := svc.AddSylveNetworkToHookAtEnd(content, "echo one")
	second := svc.AddSylveNetworkToHookAtEnd(first, "echo two")

	if got := strings.Count(second, "### Start Sylve-Managed Network ###"); got != 1 {
		t.Fatalf("expected exactly one managed section after repeated updates, got %d\n%s", got, second)
	}

	if !strings.Contains(second, "echo two") || strings.Contains(second, "echo one") {
		t.Fatalf("expected managed content to be replaced on reapply, got:\n%s", second)
	}
}

func TestAddSylveNetworkToHookAtEndWithEmptyNetworkContentRemovesManagedSection(t *testing.T) {
	svc := &Service{}

	content := "#!/bin/sh\n### Start User-Managed Hook ###\necho user\n### End User-Managed Hook ###\n"
	withManaged := svc.AddSylveNetworkToHookAtEnd(content, "echo managed")
	out := svc.AddSylveNetworkToHookAtEnd(withManaged, "")

	if strings.Contains(out, "### Start Sylve-Managed Network ###") {
		t.Fatalf("expected managed network section to be removed when content is empty, got:\n%s", out)
	}

	if !strings.Contains(out, "### Start User-Managed Hook ###") {
		t.Fatalf("expected user-managed section to be preserved, got:\n%s", out)
	}
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"strings"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
)

func TestFormatJailNetworksShowsHostInterface(t *testing.T) {
	output := formatJailNetworks(&jailModels.Jail{
		CTID: 101,
		Name: "router",
		Networks: []jailModels.Network{{
			ID: 7, Name: "wan", SwitchType: networkAttachment.SwitchTypeHostInterface, HostInterface: "ixl0.10",
		}},
	})
	for _, want := range []string{"ixl0.10", "host_interface", "hardware"} {
		if !strings.Contains(output, want) {
			t.Fatalf("networks table is missing %q:\n%s", want, output)
		}
	}
}

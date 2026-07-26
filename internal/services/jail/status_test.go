// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"testing"

	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
)

func TestCountActiveManagedJails(t *testing.T) {
	states := []jailServiceInterfaces.State{
		{CTID: 101, State: "ACTIVE"},
		{CTID: 102, State: "INACTIVE"},
		{CTID: 999, State: "ACTIVE"},
	}
	if got := countActiveManagedJails([]uint{101, 102}, states); got != 1 {
		t.Fatalf("active managed jails = %d, want 1", got)
	}
}

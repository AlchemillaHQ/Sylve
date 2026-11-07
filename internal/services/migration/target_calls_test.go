// SPDX-License-Identifier: BSD-2-Clause

package migration

import "testing"

func TestTargetDatasetRequestAcceptsOnlyTheGuestsRoots(t *testing.T) {
	vm := func(dataset string) TargetDatasetRequest {
		return TargetDatasetRequest{GuestType: "vm", GuestID: 40, Dataset: dataset}
	}
	jail := func(dataset string) TargetDatasetRequest {
		return TargetDatasetRequest{GuestType: "jail", GuestID: 40, Dataset: dataset}
	}
	for _, test := range []struct {
		req  TargetDatasetRequest
		want bool
	}{
		{vm("tank/sylve/virtual-machines/40"), true},
		{vm("tank/sylve/virtual-machines/40.merge-2"), true},
		{vm("tank/sylve/virtual-machines/40.merge-0"), false},
		{vm("tank/sylve/virtual-machines/40.merge-01"), false},
		{vm("tank/sylve/virtual-machines/400"), false},
		{vm("tank/sylve/virtual-machines/40/raw-1"), false},
		{vm("tank/sylve/jails/40"), false},
		{vm("tank/data/virtual-machines/40"), false},
		{vm("tank/sylve/virtual-machines/40@snap"), false},
		{jail("tank/sylve/jails/40"), true},
		{jail("tank/sylve/jails/40.merge-1"), false},
		{TargetDatasetRequest{GuestType: "vm", Dataset: "tank/sylve/virtual-machines/0"}, false},
	} {
		if got := test.req.Valid(); got != test.want {
			t.Errorf("%s %q valid = %t, want %t", test.req.GuestType, test.req.Dataset, got, test.want)
		}
	}
}

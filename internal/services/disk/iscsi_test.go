// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import "testing"

func TestParseISCSICAMDevices(t *testing.T) {
	output := `scbus0 on ahc0 bus 0:
<SEAGATE ST1200MM0009> at scbus0 target 0 lun 0 (pass0,da0)
scbus6 on iscsi0 bus 0:
<FREEBSD CTLDISK 0001> at scbus6 target 0 lun 0 (pass1,da1)
`

	devices := parseISCSICAMDevices(output)
	if _, ok := devices["da1"]; !ok {
		t.Fatal("expected da1 to be identified as iSCSI")
	}
	if _, ok := devices["da0"]; ok {
		t.Fatal("did not expect SAS disk da0 to be identified as iSCSI")
	}
}

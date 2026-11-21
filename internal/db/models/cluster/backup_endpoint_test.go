// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import "testing"

func TestBackupTargetZeltaEndpointBracketsIPv6(t *testing.T) {
	for host, want := range map[string]string{
		"root@192.0.2.10":     "root@192.0.2.10:tank/backups/vm/107",
		"root@2001:db8::10":   "root@[2001:db8::10]:tank/backups/vm/107",
		"root@[2001:db8::10]": "root@[2001:db8::10]:tank/backups/vm/107",
		"backup.example.org":  "backup.example.org:tank/backups/vm/107",
	} {
		target := BackupTarget{SSHHost: host, BackupRoot: "tank/backups"}
		if got := target.ZeltaEndpoint("vm/107"); got != want {
			t.Errorf("ZeltaEndpoint for %q = %q, want %q", host, got, want)
		}
	}
}

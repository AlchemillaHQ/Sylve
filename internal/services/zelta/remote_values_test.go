// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestCanonicalZeltaEndpointsPreserveIPv6ArgumentBoundaries(t *testing.T) {
	target := &clusterModels.BackupTarget{
		SSHHost:    "root@[2001:0db8::1]",
		SSHPort:    22,
		BackupRoot: "tank/backups",
	}
	endpoint, err := canonicalZeltaEndpoint(target, "virtual-machines/107")
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	if target.SSHHost != "root@2001:db8::1" || endpoint != "root@[2001:db8::1]:tank/backups/virtual-machines/107" {
		t.Fatalf("target=%+v endpoint=%q", target, endpoint)
	}

	snapshotEndpoint, err := canonicalZeltaSnapshotEndpoint(
		target,
		"tank/backups/virtual-machines/107",
		"@bk_j1_c1_test",
	)
	if err != nil {
		t.Fatalf("snapshot endpoint: %v", err)
	}
	if snapshotEndpoint != "root@[2001:db8::1]:tank/backups/virtual-machines/107@bk_j1_c1_test" {
		t.Fatalf("snapshot endpoint=%q", snapshotEndpoint)
	}
	if dataset := datasetFromZeltaEndpoint("root@[2001:db8::1]:tank:archive/backups"); dataset != "tank:archive/backups" {
		t.Fatalf("IPv6 endpoint dataset=%q", dataset)
	}
	if dataset := datasetFromZeltaEndpoint("root@backup:tank:archive/backups"); dataset != "tank:archive/backups" {
		t.Fatalf("DNS endpoint dataset=%q", dataset)
	}
}

func TestCanonicalReplicationTransferValuesRejectsNonExactInput(t *testing.T) {
	target := &clusterModels.BackupTarget{
		SSHHost:    "root@backup",
		SSHPort:    22,
		BackupRoot: "tank/backups",
	}
	source, destination, err := canonicalReplicationTransferValues(
		target,
		"zroot/sylve/virtual-machines/107",
		"virtual-machines/107",
	)
	if err != nil {
		t.Fatalf("canonical transfer: %v", err)
	}
	if source.String() != "zroot/sylve/virtual-machines/107" || destination.String() != "tank/backups/virtual-machines/107" {
		t.Fatalf("canonical transfer source=%q destination=%q", source.String(), destination.String())
	}

	for _, test := range []struct {
		source string
		suffix string
	}{
		{source: "/zroot/sylve/virtual-machines/107", suffix: "virtual-machines/107"},
		{source: "zroot/sylve/virtual-machines/107", suffix: "/virtual-machines/107"},
		{source: "zroot/sylve/virtual-machines/107", suffix: "virtual-machines/../107"},
		{source: "zroot/sylve/virtual-machines/107", suffix: ""},
	} {
		if _, _, err := canonicalReplicationTransferValues(target, test.source, test.suffix); err == nil {
			t.Fatalf("accepted source=%q suffix=%q", test.source, test.suffix)
		}
	}

	badTarget := *target
	badTarget.SSHHost = "-oProxyCommand=touch"
	if _, _, err := canonicalReplicationTransferValues(&badTarget, "zroot/data", "data"); err == nil || !strings.Contains(err.Error(), "invalid_ssh_host") {
		t.Fatalf("invalid target error=%v", err)
	}
}

func TestCanonicalZFSPropertiesRejectDuplicateCanonicalNames(t *testing.T) {
	if _, err := canonicalZFSProperties(map[string]string{
		"readonly":   "on",
		" readonly ": "off",
	}); err == nil || !strings.Contains(err.Error(), "duplicate_zfs_property") {
		t.Fatalf("duplicate map property error=%v", err)
	}

	if _, err := canonicalZFSPropertyAssignments([]string{
		"readonly=on",
		" readonly =off",
	}); err == nil || !strings.Contains(err.Error(), "duplicate_zfs_property") {
		t.Fatalf("duplicate assignment property error=%v", err)
	}
}

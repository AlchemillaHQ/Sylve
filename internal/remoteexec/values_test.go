// SPDX-License-Identifier: BSD-2-Clause

package remoteexec

import (
	"strings"
	"testing"
)

func TestParseSSHDestination(t *testing.T) {
	tests := map[string]string{
		"backup":                  "backup",
		"root@Backup.Example":     "root@backup.example",
		"192.0.2.10":              "192.0.2.10",
		"root@2001:db8::1":        "root@2001:db8::1",
		"root@[2001:0db8::1]":     "root@2001:db8::1",
		"backup.example.invalid.": "backup.example.invalid.",
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			got, err := ParseSSHDestination(input)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got.String() != want {
				t.Fatalf("destination = %q, want %q", got.String(), want)
			}
			wantZelta := want
			if strings.Contains(want, ":") {
				if user, host, found := strings.Cut(want, "@"); found {
					wantZelta = user + "@[" + host + "]"
				} else {
					wantZelta = "[" + want + "]"
				}
			}
			if got.ZeltaString() != wantZelta {
				t.Fatalf("Zelta destination = %q, want %q", got.ZeltaString(), wantZelta)
			}
		})
	}
}

func TestParseSSHDestinationRejectsUnsafeOrAmbiguousInput(t *testing.T) {
	for _, input := range []string{
		"", "-oProxyCommand=touch", "root@-host", "root@@host", "@host",
		"host:22", "[2001:db8::1]:22", "[backup]", "host name", "host\nname",
		"host;touch", "host'quote", `host"quote`, "$(hostname)", "host/path", "bad_host", "123", "2130706433",
		"999.999.999.999", "-host",
	} {
		t.Run(input, func(t *testing.T) {
			if got, err := ParseSSHDestination(input); err == nil {
				t.Fatalf("accepted %q as %q", input, got.String())
			}
		})
	}
}

func TestParseZFSDataset(t *testing.T) {
	for input, want := range map[string]string{
		"tank":                   "tank",
		"tank/backups/vm-107":    "tank/backups/vm-107",
		" Tank/backups:archive ": "Tank/backups:archive",
	} {
		t.Run(input, func(t *testing.T) {
			dataset, err := ParseZFSDataset(input)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if dataset.String() != want {
				t.Fatalf("dataset = %q, want %q", dataset.String(), want)
			}
		})
	}
}

func TestParseZFSDatasetRejectsNonExactInput(t *testing.T) {
	for _, input := range []string{
		"", "/tank/backups", "tank/backups/", "tank//backups", "tank/./backups",
		"tank/../backups", "tank/backups@snap", "tank/backups#bookmark", "tank/bad;name",
		"tank/bad'name", `tank/bad"name`, "tank/$(touch)", "tank/recv%temporary", "tank/backup with spaces", "tank/backups\nnext",
		"1tank/backups",
	} {
		t.Run(input, func(t *testing.T) {
			if got, err := ParseZFSDataset(input); err == nil {
				t.Fatalf("accepted %q as %q", input, got.String())
			}
		})
	}
}

func TestZFSDatasetRelationships(t *testing.T) {
	root, _ := ParseZFSDataset("tank/backups")
	child, err := JoinZFSDataset(root, "vm/107")
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if child.String() != "tank/backups/vm/107" || child.Pool() != "tank" || !child.Within(root) {
		t.Fatalf("unexpected child: %#v", child)
	}
	sibling, _ := ParseZFSDataset("tank/backups-old")
	if sibling.Within(root) {
		t.Fatal("ancestor-prefix sibling classified within root")
	}
}

func TestParseZFSSnapshot(t *testing.T) {
	name, err := ParseZFSSnapshotName("@bk_107:one")
	if err != nil || name.String() != "bk_107:one" || name.WithAt() != "@bk_107:one" {
		t.Fatalf("snapshot name = %#v err=%v", name, err)
	}
	full, err := ParseZFSSnapshot("tank/backups@bk_107:one")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if full.String() != "tank/backups@bk_107:one" || full.Dataset().String() != "tank/backups" {
		t.Fatalf("snapshot = %#v", full)
	}
}

func TestParseZFSSnapshotRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"", "@", "snap/name", "snap@again", "snap#bookmark", "snap;touch", "snap'quote", `snap"quote`, "$(touch)", "snap with spaces", "snap\nnext"} {
		t.Run(input, func(t *testing.T) {
			if got, err := ParseZFSSnapshotName(input); err == nil {
				t.Fatalf("accepted %q as %q", input, got.String())
			}
		})
	}
	for _, input := range []string{"tank/backups", "tank/backups@", "tank/backups@snap@again", "tank//backups@snap", "tank/backups#mark@snap"} {
		t.Run(input, func(t *testing.T) {
			if got, err := ParseZFSSnapshot(input); err == nil {
				t.Fatalf("accepted %q as %q", input, got.String())
			}
		})
	}
}

func TestParseZFSPropertyValuesAndOperationTokens(t *testing.T) {
	for _, input := range []string{"readonly", "sylve:replication-policy-id", "org.example:backup_state"} {
		property, err := ParseZFSPropertyName(input)
		if err != nil || property.String() != input {
			t.Fatalf("property %q = %#v err=%v", input, property, err)
		}
	}
	for _, input := range []string{"", "Readonly", "-readonly", "sylve:bad value", "sylve:bad=value", "sylve:bad'quote", `sylve:bad"quote`, "sylve:$(bad)", "sylve:bad\nname"} {
		if property, err := ParseZFSPropertyName(input); err == nil {
			t.Fatalf("accepted property %q as %q", input, property.String())
		}
	}
	for _, input := range []string{"", "value with spaces", "quote'\";$()", "line one\nline two"} {
		value, err := ParseZFSPropertyValue(input)
		if err != nil || value.String() != input {
			t.Fatalf("property value %q = %#v err=%v", input, value, err)
		}
	}
	if _, err := ParseZFSPropertyValue("bad\x00value"); err == nil {
		t.Fatal("accepted NUL-bearing property value")
	}
	for _, input := range []string{"replication:node-1:uuid", "target-restore:local:uuid", "migration.run_1"} {
		token, err := ParseOperationToken(input)
		if err != nil || token.String() != input {
			t.Fatalf("token %q = %#v err=%v", input, token, err)
		}
	}
	for _, input := range []string{"", "-option", "token with space", "token;touch", "token'quote", `token"quote`, "$(touch)", "token\nnext", strings.Repeat("a", maxOperationTokenLen+1)} {
		if token, err := ParseOperationToken(input); err == nil {
			t.Fatalf("accepted token %q as %q", input, token.String())
		}
	}
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestBuildBackupTargetDatasetInfosUsesRootsAndKeepsLegacyBrowsable(t *testing.T) {
	const filesystemOutput = `backup	off
backup/pool	off
backup/pool/sylve	off
backup/pool/sylve/jails/102	off
backup/pool/sylve/jails/102/j-abc	off
backup/pool/sylve/jails/102/j-abc/active	aes-256-gcm
backup/pool/sylve/jails/102/j-abc/active/child	off
backup/pool/sylve/jails/102/j-abc/active_gen-old	aes-256-gcm
backup/legacy	off
backup/legacy/tree	off
`
	datasets := buildBackupTargetDatasetInfos(
		"backup",
		filesystemOutput,
		"backup/pool/sylve/jails/102/j-abc/active\t42\n",
	)

	names := make(map[string]BackupTargetDatasetInfo, len(datasets))
	for _, dataset := range datasets {
		names[dataset.Name] = dataset
		if dataset.SnapshotCountKnown {
			t.Fatalf("snapshot count unexpectedly marked known for %q", dataset.Name)
		}
	}
	for _, want := range []string{
		"backup/pool/sylve/jails/102/j-abc/active",
		"backup/pool/sylve/jails/102/j-abc/active_gen-old",
		"backup/legacy",
		"backup/legacy/tree",
	} {
		if _, ok := names[want]; !ok {
			t.Fatalf("missing dataset %q from %#v", want, names)
		}
	}
	for _, unwanted := range []string{
		"backup/pool",
		"backup/pool/sylve/jails/102/j-abc",
		"backup/pool/sylve/jails/102/j-abc/active/child",
	} {
		if _, ok := names[unwanted]; ok {
			t.Fatalf("managed ancestor or child %q should be hidden", unwanted)
		}
	}
	if !names["backup/pool/sylve/jails/102/j-abc/active"].Encrypted {
		t.Fatal("root encryption state was not preserved")
	}
	if got := names["backup/pool/sylve/jails/102/j-abc/active_gen-old"].Lineage; got != "rotated" {
		t.Fatalf("rotated lineage = %q", got)
	}
}

func TestListRemoteTargetDatasetsDoesNotEnumerateSnapshots(t *testing.T) {
	harness := newFakeSSHHarness(t)
	harness.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
		"zfs list -t filesystem -r -Hp -o name,encryption backup": {{
			Stdout: "backup\toff\nbackup/data/j-1/active\toff\n",
		}},
		"zfs get -H -p -r -t filesystem -s local -o name,value sylve:backup_job_id backup": {{
			Stdout: "backup/data/j-1/active\t1\n",
		}},
	}})

	database := newZeltaServiceTestDB(t)
	target := clusterModels.BackupTarget{
		Name:       "target",
		SSHHost:    "user@target",
		BackupRoot: "backup",
		Enabled:    true,
	}
	if err := database.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}

	datasets, err := newTestZeltaService(database).ListRemoteTargetDatasets(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("list target datasets: %v", err)
	}
	if len(datasets) != 1 || datasets[0].Name != "backup/data/j-1/active" {
		t.Fatalf("datasets = %#v", datasets)
	}
	calls := harness.Calls()
	if len(calls) != 2 {
		t.Fatalf("SSH calls = %d, want 2: %v", len(calls), calls)
	}
	for _, call := range calls {
		if strings.Contains(call, "-t snapshot") {
			t.Fatalf("target discovery enumerated snapshots: %s", call)
		}
	}
}

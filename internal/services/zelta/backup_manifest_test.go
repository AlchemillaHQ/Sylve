// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestBuildBackupSnapshotManifestEntriesRequiresCompleteSourceTree(t *testing.T) {
	datasets, err := parseBackupDatasetTree(
		"pool/source\tfilesystem\npool/source/child\tvolume\n",
		"pool/source",
		true,
	)
	if err != nil {
		t.Fatalf("parse dataset tree: %v", err)
	}
	guids, err := parseBackupSnapshotGUIDs(
		"pool/source@bk_j1_run\t101\n",
		"pool/source",
		"bk_j1_run",
	)
	if err != nil {
		t.Fatalf("parse snapshot GUIDs: %v", err)
	}

	_, err = buildBackupSnapshotManifestEntries(
		datasets,
		guids,
		"pool/source",
		"pool/source",
		true,
	)
	if err == nil || !strings.Contains(err.Error(), "backup_manifest_snapshot_missing:pool/source/child") {
		t.Fatalf("expected missing child snapshot failure, got %v", err)
	}
}

func TestHistoricalBackupManifestIgnoresLaterDatasetWithoutSelectedSnapshot(t *testing.T) {
	sourceDatasets, err := parseBackupDatasetTree(
		"pool/source\tfilesystem\npool/source/original\tfilesystem\n",
		"pool/source",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	sourceGUIDs, err := parseBackupSnapshotGUIDs(
		"pool/source@bk_j1_old\t101\npool/source/original@bk_j1_old\t202\n",
		"pool/source",
		"bk_j1_old",
	)
	if err != nil {
		t.Fatal(err)
	}
	sourceEntries, err := buildBackupSnapshotManifestEntries(
		sourceDatasets,
		sourceGUIDs,
		"pool/source",
		"pool/source",
		true,
	)
	if err != nil {
		t.Fatalf("source manifest: %v", err)
	}

	targetDatasets, err := parseBackupDatasetTree(
		"backup/active\tfilesystem\nbackup/active/original\tfilesystem\nbackup/active/later\tfilesystem\n",
		"backup/active",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	targetGUIDs, err := parseBackupSnapshotGUIDs(
		"backup/active@bk_j1_old\t101\n"+
			"backup/active/original@bk_j1_old\t202\n"+
			"backup/active/later@bk_j1_new\t303\n",
		"backup/active",
		"bk_j1_old",
	)
	if err != nil {
		t.Fatal(err)
	}
	targetEntries, err := buildBackupSnapshotManifestEntries(
		targetDatasets,
		targetGUIDs,
		"backup/active",
		"pool/source",
		false,
	)
	if err != nil {
		t.Fatalf("target manifest: %v", err)
	}

	sourceManifest, err := buildBackupManifest(1, "bk_j1_old", true, sourceEntries)
	if err != nil {
		t.Fatal(err)
	}
	targetManifest, err := buildBackupManifest(1, "bk_j1_old", true, targetEntries)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := backupManifestHash(targetManifest), backupManifestHash(sourceManifest); got != want {
		t.Fatalf("historical manifest changed after later child: got %s want %s", got, want)
	}
}

func TestBackupManifestHashDetectsEntireMissingChild(t *testing.T) {
	source, err := buildBackupManifest(9, "bk_j9_run", true, []backupManifestEntry{
		{Root: "pool/source", Type: "filesystem", SnapshotGUID: "101"},
		{Root: "pool/source", Suffix: "child", Type: "filesystem", SnapshotGUID: "202"},
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := buildBackupManifest(9, "bk_j9_run", true, []backupManifestEntry{
		{Root: "pool/source", Type: "filesystem", SnapshotGUID: "101"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if backupManifestHash(source) == backupManifestHash(target) {
		t.Fatal("missing child must change committed manifest hash")
	}
}

func TestBackupManifestHashIsStableAcrossInputOrder(t *testing.T) {
	left, err := buildBackupManifest(12, "bk_jc_run", true, []backupManifestEntry{
		{Root: "pool/b", Type: "volume", SnapshotGUID: "3"},
		{Root: "pool/a", Suffix: "child", Type: "filesystem", SnapshotGUID: "2"},
		{Root: "pool/a", Type: "filesystem", SnapshotGUID: "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := buildBackupManifest(12, "bk_jc_run", true, []backupManifestEntry{
		{Root: "pool/a", Type: "filesystem", SnapshotGUID: "1"},
		{Root: "pool/b", Type: "volume", SnapshotGUID: "3"},
		{Root: "pool/a", Suffix: "child", Type: "filesystem", SnapshotGUID: "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := backupManifestHash(left), backupManifestHash(right); got != want {
		t.Fatalf("manifest hash is order dependent: got %s want %s", got, want)
	}
}

func TestBackupCommitPropertiesRoundTripRoots(t *testing.T) {
	manifest, err := buildBackupManifest(35, "bk_jz_run", true, []backupManifestEntry{
		{Root: "pool/vm/100", Type: "filesystem", SnapshotGUID: "1"},
		{Root: "other/vm/100", Type: "volume", SnapshotGUID: "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata := newBackupCommitMetadata(manifest)
	properties, err := backupCommitProperties(metadata)
	if err != nil {
		t.Fatalf("commit properties: %v", err)
	}
	if len(properties) != 7 {
		t.Fatalf("unexpected property count: %d", len(properties))
	}

	var encodedRoots string
	for _, property := range properties {
		if strings.HasPrefix(property, backupCommitPropertyRoots+"=") {
			encodedRoots = strings.TrimPrefix(property, backupCommitPropertyRoots+"=")
		}
	}
	gotRoots, err := parseBackupManifestRootsValue(encodedRoots)
	if err != nil {
		t.Fatalf("parse roots: %v", err)
	}
	if !reflect.DeepEqual(gotRoots, manifest.Roots) {
		t.Fatalf("roots mismatch: got %v want %v", gotRoots, manifest.Roots)
	}
}

func TestParseBackupDatasetTreeRejectsDescendantForNonrecursiveJob(t *testing.T) {
	_, err := parseBackupDatasetTree(
		"pool/source\tfilesystem\npool/source/child\tfilesystem\n",
		"pool/source",
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "backup_manifest_nonrecursive_descendant") {
		t.Fatalf("expected nonrecursive descendant rejection, got %v", err)
	}
}

func TestParseBackupCommitMetadataRoundTrip(t *testing.T) {
	manifest, err := buildBackupManifest(17, "bk_jh_run", true, []backupManifestEntry{
		{Root: "pool/source", Type: "filesystem", SnapshotGUID: "101"},
		{Root: "pool/source", Suffix: "child", Type: "volume", SnapshotGUID: "202"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := newBackupCommitMetadata(manifest)
	properties, err := backupCommitProperties(want)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for _, property := range properties {
		parts := strings.SplitN(property, "=", 2)
		output.WriteString(parts[0])
		output.WriteByte('\t')
		output.WriteString(parts[1])
		output.WriteString("\tlocal")
		output.WriteByte('\n')
	}
	got, err := parseBackupCommitMetadata(output.String())
	if err != nil {
		t.Fatalf("parse commit metadata: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commit metadata mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestValidateBackupCommitForJobRejectsOtherJob(t *testing.T) {
	manifest, err := buildBackupManifest(17, "bk_jh_run", true, []backupManifestEntry{
		{Root: "pool/source", Type: "filesystem", SnapshotGUID: "101"},
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata := newBackupCommitMetadata(manifest)
	job := &clusterModels.BackupJob{ID: 18, Recursive: true}
	if err := validateBackupCommitForJob(metadata, job, "bk_jh_run"); err == nil || !strings.Contains(err.Error(), "job_mismatch") {
		t.Fatalf("expected job mismatch, got %v", err)
	}
}

func TestBackupCommitMetadataRoundTripsOnRealZFSSnapshot(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	dataset := pool + "/backup-commit"
	snapshotName := "bk_jh_real"
	zfstest.EnsureDataset(t, client, dataset)
	if output, err := exec.Command("zfs", "snapshot", dataset+"@"+snapshotName).CombinedOutput(); err != nil {
		t.Fatalf("create snapshot: %v\n%s", err, output)
	}

	manifest, err := buildBackupManifest(17, snapshotName, true, []backupManifestEntry{
		{Root: dataset, Type: "filesystem", SnapshotGUID: "101"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := newBackupCommitMetadata(manifest)
	properties, err := backupCommitProperties(want)
	if err != nil {
		t.Fatal(err)
	}
	setArgs := append([]string{"set"}, properties...)
	setArgs = append(setArgs, dataset+"@"+snapshotName)
	if output, err := exec.Command("zfs", setArgs...).CombinedOutput(); err != nil {
		t.Fatalf("set snapshot commit properties: %v\n%s", err, output)
	}

	output, err := exec.Command(
		"zfs", "get", "-H", "-p", "-o", "property,value,source",
		strings.Join(backupCommitPropertyNames(), ","),
		dataset+"@"+snapshotName,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("get snapshot commit properties: %v\n%s", err, output)
	}
	got, err := parseBackupCommitMetadata(string(output))
	if err != nil {
		t.Fatalf("parse snapshot commit properties: %v\n%s", err, output)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot commit metadata mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestParseBackupCommitMetadataRejectsInheritedAndDuplicateProperties(t *testing.T) {
	manifest, err := buildBackupManifest(17, "bk_jh_run", true, []backupManifestEntry{
		{Root: "pool/source", Type: "filesystem", SnapshotGUID: "101"},
	})
	if err != nil {
		t.Fatal(err)
	}
	properties, err := backupCommitProperties(newBackupCommitMetadata(manifest))
	if err != nil {
		t.Fatal(err)
	}

	lines := make([]string, 0, len(properties))
	for _, property := range properties {
		parts := strings.SplitN(property, "=", 2)
		lines = append(lines, parts[0]+"\t"+parts[1]+"\tlocal")
	}

	inherited := append([]string(nil), lines...)
	inherited[0] = strings.TrimSuffix(inherited[0], "\tlocal") + "\tinherited from pool/backup"
	if _, err := parseBackupCommitMetadata(strings.Join(inherited, "\n")); err == nil || !strings.Contains(err.Error(), "not_local") {
		t.Fatalf("expected inherited commit property rejection, got %v", err)
	}

	duplicate := append(append([]string(nil), lines...), lines[0])
	if _, err := parseBackupCommitMetadata(strings.Join(duplicate, "\n")); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate commit property rejection, got %v", err)
	}

	unknown := append(append([]string(nil), lines...), "sylve:other\tvalue\tlocal")
	if _, err := parseBackupCommitMetadata(strings.Join(unknown, "\n")); err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("expected unknown commit property rejection, got %v", err)
	}
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func requireValidShellScript(t *testing.T, script string) {
	t.Helper()
	if strings.Contains(script, "%!") {
		t.Fatalf("generated script has an unresolved formatting operand:\n%s", script)
	}
	if output, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("invalid shell script: %v\n%s\n%s", err, string(output), script)
	}
}

func TestRemotePOSIXShellCommandWorksThroughPOSIXAndCShells(t *testing.T) {
	script := "set -eu\nvalue='quoted value'\nprintf '%s' \"$value\""
	command := remotePOSIXShellCommand(script)
	tests := []struct {
		name string
		path string
		args []string
	}{
		{name: "posix", path: "/bin/sh", args: []string{"-c", command}},
		{name: "csh", path: "/bin/csh", args: []string{"-f", "-c", command}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := exec.LookPath(tt.path); err != nil {
				t.Skipf("%s unavailable: %v", tt.path, err)
			}
			output, err := exec.Command(tt.path, tt.args...).CombinedOutput()
			if err != nil {
				t.Fatalf("remote script transport failed through %s: %v\n%s", tt.name, err, output)
			}
			if got := string(output); got != "quoted value" {
				t.Fatalf("remote script output through %s=%q, want %q", tt.name, got, "quoted value")
			}
		})
	}
}

func TestRunReplicationAttemptsExhaustionDoesNotReturnSuccess(t *testing.T) {
	attempts := 0
	aborts := 0
	err := runReplicationAttempts(
		3,
		false,
		replicationAttemptHooks{
			Run: func(bool) (string, error) {
				attempts++
				return "", errors.New("cannot receive resume stream: destination is in partially-complete state")
			},
			Abort: func() (string, error) {
				aborts++
				return "", nil
			},
		},
		nil,
	)
	if err == nil {
		t.Fatal("expected retry exhaustion to fail")
	}
	if !strings.Contains(err.Error(), "replication_retry_exhausted_after_3_attempts") {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if aborts != 3 {
		t.Fatalf("expected 3 safe abort attempts, got %d", aborts)
	}
}

func TestRunReplicationAttemptsCompletesOnFinalAttempt(t *testing.T) {
	attempts := 0
	err := runReplicationAttempts(
		3,
		false,
		replicationAttemptHooks{
			Run: func(bool) (string, error) {
				attempts++
				if attempts < 3 {
					return "", errors.New("broken pipe")
				}
				return "received", nil
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("expected final attempt to succeed: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestRunReplicationAttemptsLegacyDivergenceFailsClosed(t *testing.T) {
	resetCalled := false
	err := runReplicationAttempts(
		3,
		false,
		replicationAttemptHooks{
			Run: func(bool) (string, error) {
				return "", errors.New("destination has been modified")
			},
			Reset: func() error {
				resetCalled = true
				return nil
			},
		},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "requires_staged_reseed") {
		t.Fatalf("expected staged reseed requirement, got %v", err)
	}
	if resetCalled {
		t.Fatal("legacy divergence must never reset an unproven target")
	}
}

func TestRunReplicationAttemptsResetsOnlyProvenStaging(t *testing.T) {
	attempts := 0
	resetCalled := 0
	err := runReplicationAttempts(
		3,
		true,
		replicationAttemptHooks{
			Run: func(bool) (string, error) {
				attempts++
				if attempts == 1 {
					return "", errors.New("destination has snapshots; must destroy")
				}
				return "", nil
			},
			AuthorizeForce: func() error { return nil },
			Reset: func() error {
				resetCalled++
				return nil
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("expected proven staging reset to recover: %v", err)
	}
	if resetCalled != 1 {
		t.Fatalf("expected one guarded reset, got %d", resetCalled)
	}
}

func TestRunReplicationAttemptsRefusesForceWithoutCurrentProvenance(t *testing.T) {
	attempts := 0
	err := runReplicationAttempts(
		3,
		true,
		replicationAttemptHooks{
			Run: func(bool) (string, error) {
				attempts++
				return "", errors.New("destination has been modified")
			},
			AuthorizeForce: func() error {
				return errors.New("run id mismatch")
			},
		},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "force_receive_provenance_failed") {
		t.Fatalf("expected force receive provenance failure, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("unproven force receive must not start; attempts=%d", attempts)
	}
}

func TestPrepareReplicationTransferMetadataFailsClosed(t *testing.T) {
	_, _, err := prepareReplicationTransferMetadata(
		func() (string, error) { return "", errors.New("ssh unavailable") },
		func() (bool, error) { return false, nil },
	)
	if err == nil || !strings.Contains(err.Error(), "common_snapshot_lookup_failed") {
		t.Fatalf("expected common snapshot failure, got %v", err)
	}

	_, _, err = prepareReplicationTransferMetadata(
		func() (string, error) { return "ha_base", nil },
		func() (bool, error) { return false, errors.New("zfs get failed") },
	)
	if err == nil || !strings.Contains(err.Error(), "encryption_check_failed") {
		t.Fatalf("expected encryption detection failure, got %v", err)
	}
}

func TestForceReceiveRequiresCompleteProvenance(t *testing.T) {
	if hasCompleteReplicationProvenance(map[string]string{
		replicationPropertyPolicyID: "7",
	}) {
		t.Fatal("partial provenance must not authorize force receive")
	}
	opts := ReplicationZFSTransferOptions{
		PolicyID:     7,
		RunID:        "run-1",
		OwnerEpoch:   9,
		SnapshotName: "ha_shared",
		SnapshotGUID: "100",
	}
	properties := replicationProvenanceProperties(opts, "tank/source", "tank/target", replicationStateReceiving)
	if !hasCompleteReplicationProvenance(properties) {
		t.Fatalf("complete provenance was rejected: %#v", properties)
	}
}

func TestRequireReplicationReadonlyFailsClosed(t *testing.T) {
	err := requireReplicationReadonly(func() error { return errors.New("permission denied") })
	if err == nil || !strings.Contains(err.Error(), "readonly_hardening_failed") {
		t.Fatalf("expected readonly hardening failure, got %v", err)
	}
}

func TestParseReplicationSnapshotIdentities(t *testing.T) {
	output := strings.Join([]string{
		"tank/vm@manual\t11",
		"tank/vm@ha_old\t101",
		"tank/vm/child@ha_old\t102",
		"tank/vm@ha_new\t201",
	}, "\n")
	got, err := parseReplicationSnapshotIdentities(output, "tank/vm")
	if err != nil {
		t.Fatalf("parse identities: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 root HA snapshots, got %#v", got)
	}
	if got[0].Name != "ha_old" || got[0].GUID != "101" || got[1].Name != "ha_new" || got[1].GUID != "201" {
		t.Fatalf("unexpected identities: %#v", got)
	}
}

func TestParseReplicationSnapshotIdentitiesRejectsMissingGUID(t *testing.T) {
	_, err := parseReplicationSnapshotIdentities("tank/vm@ha_bad\t-", "tank/vm")
	if err == nil || !strings.Contains(err.Error(), "invalid_replication_snapshot_identity") {
		t.Fatalf("expected invalid identity error, got %v", err)
	}
}

func TestReplicationSnapshotTreeManifestIncludesDescendants(t *testing.T) {
	datasets, err := parseReplicationDatasetTree(strings.Join([]string{
		"tank/target",
		"tank/target/child-fs",
		"tank/target/child-vol",
	}, "\n"), "tank/target")
	if err != nil {
		t.Fatalf("parse dataset tree: %v", err)
	}
	guids, err := parseReplicationSnapshotTreeGUIDs(strings.Join([]string{
		"tank/target@ha_generation\t101",
		"tank/target/child-fs@ha_generation\t102",
		"tank/target/child-vol@ha_generation\t103",
		"tank/target/child-vol@ha_older\t99",
	}, "\n"), "tank/target", "ha_generation")
	if err != nil {
		t.Fatalf("parse snapshot tree: %v", err)
	}
	manifest, err := buildReplicationSnapshotTreeManifest(
		datasets,
		guids,
		"tank/target",
		"zroot/source",
		"ha_generation",
	)
	if err != nil {
		t.Fatalf("build tree manifest: %v", err)
	}
	want := []ReplicationSnapshotManifestEntry{
		{SourceDataset: "zroot/source", SnapshotName: "ha_generation", SnapshotGUID: "101"},
		{SourceDataset: "zroot/source/child-fs", SnapshotName: "ha_generation", SnapshotGUID: "102"},
		{SourceDataset: "zroot/source/child-vol", SnapshotName: "ha_generation", SnapshotGUID: "103"},
	}
	if !replicationSnapshotTreeManifestsEqual(manifest, want) {
		t.Fatalf("unexpected recursive manifest: %#v", manifest)
	}
}

func TestReplicationSnapshotTreeManifestRejectsMissingOrChangedDescendant(t *testing.T) {
	datasets := []string{"tank/root", "tank/root/child"}
	if _, err := buildReplicationSnapshotTreeManifest(
		datasets,
		map[string]string{"tank/root": "1"},
		"tank/root",
		"tank/root",
		"ha_generation",
	); err == nil {
		t.Fatal("missing descendant snapshot was accepted")
	}
	left := []ReplicationSnapshotManifestEntry{
		{SourceDataset: "tank/root", SnapshotName: "ha_generation", SnapshotGUID: "1"},
		{SourceDataset: "tank/root/child", SnapshotName: "ha_generation", SnapshotGUID: "2"},
	}
	right := append([]ReplicationSnapshotManifestEntry(nil), left...)
	right[1].SnapshotGUID = "changed"
	if replicationSnapshotTreeManifestsEqual(left, right) {
		t.Fatal("changed descendant GUID was accepted")
	}
	if replicationSnapshotTreeManifestsEqual(left, left[:1]) {
		t.Fatal("missing descendant was accepted")
	}
}

func TestLatestCommonReplicationSnapshotUsesGUID(t *testing.T) {
	local := []replicationSnapshotIdentity{
		{Name: "ha_old", GUID: "1"},
		{Name: "ha_new", GUID: "2"},
	}
	remote := []replicationSnapshotIdentity{
		{Name: "ha_old", GUID: "1"},
		{Name: "ha_new", GUID: "2"},
	}
	common, err := latestCommonReplicationSnapshot(local, remote)
	if err != nil {
		t.Fatalf("latest common: %v", err)
	}
	if common != "ha_new" {
		t.Fatalf("expected ha_new, got %q", common)
	}
}

func TestLatestCommonReplicationSnapshotRejectsSameNameDifferentGUID(t *testing.T) {
	_, err := latestCommonReplicationSnapshot(
		[]replicationSnapshotIdentity{{Name: "ha_same", GUID: "1"}},
		[]replicationSnapshotIdentity{{Name: "ha_same", GUID: "2"}},
	)
	if err == nil || !strings.Contains(err.Error(), "guid_mismatch") {
		t.Fatalf("expected GUID mismatch, got %v", err)
	}
}

func TestReplicationSnapshotGUIDRequiresExactGeneration(t *testing.T) {
	identities := []replicationSnapshotIdentity{
		{Name: "ha_old", GUID: "10"},
		{Name: "ha_shared", GUID: "20"},
	}
	guid, err := replicationSnapshotGUID(identities, "ha_shared")
	if err != nil {
		t.Fatalf("resolve GUID: %v", err)
	}
	if guid != "20" {
		t.Fatalf("expected GUID 20, got %q", guid)
	}
	if _, err := replicationSnapshotGUID(identities, "ha_missing"); err == nil {
		t.Fatal("missing generation must fail")
	}
}

func TestReplicationStagingPathsAndOptions(t *testing.T) {
	opts := ReplicationZFSTransferOptions{
		PolicyID:       7,
		RunID:          "run-abc",
		OwnerEpoch:     9,
		SnapshotName:   "ha_shared-generation",
		GenerationName: "generation-1",
	}
	if err := opts.validate(); err != nil {
		t.Fatalf("validate options: %v", err)
	}
	staging, err := replicationStagingDatasetPath("tank/sylve/virtual-machines/10", opts)
	if err != nil {
		t.Fatalf("staging path: %v", err)
	}
	if staging != "tank/sylve/virtual-machines/10_gen-generation-1" {
		t.Fatalf("unexpected staging path: %s", staging)
	}
	previous, err := replicationPreviousDatasetPath("tank/sylve/virtual-machines/10", opts)
	if err != nil {
		t.Fatalf("previous path: %v", err)
	}
	if previous != "tank/sylve/virtual-machines/10_previous-generation-1" {
		t.Fatalf("unexpected previous path: %s", previous)
	}
	if _, err := replicationStagingDatasetPath("tank/guest;zfs-destroy", opts); err == nil {
		t.Fatal("unsafe staging path was accepted")
	}
}

func TestDestroyProvenStagingScriptGuardsBeforeDestroy(t *testing.T) {
	script := buildDestroyProvenStagingScript(
		"tank/guest_gen-run",
		map[string]string{
			replicationPropertyPolicyID: "7",
			replicationPropertyRunID:    "run-1",
		},
	)
	guardIndex := strings.Index(script, "replication_provenance_mismatch")
	destroyIndex := strings.Index(script, "zfs destroy -r")
	if guardIndex < 0 || destroyIndex < 0 || guardIndex > destroyIndex {
		t.Fatalf("expected provenance guards before destroy:\n%s", script)
	}
	requireValidShellScript(t, script)
}

func TestFailedStagingCleanupProvesBeforeAndAfterAbort(t *testing.T) {
	script := buildAbortAndDestroyProvenStagingScript("tank/vm/7_gen-run", map[string]string{
		replicationPropertyPolicyID:   "7",
		replicationPropertyRunID:      "run-7",
		replicationPropertyOwnerEpoch: "3",
	})
	firstProof := strings.Index(script, "proven || { echo replication_provenance_mismatch_before_abort")
	abort := strings.Index(script, "zfs receive -A")
	secondProof := strings.Index(script, "proven || { echo replication_provenance_mismatch_after_abort")
	destroy := strings.Index(script, "zfs destroy -r")
	if firstProof < 0 || abort < 0 || secondProof < 0 || destroy < 0 ||
		!(firstProof < abort && abort < secondProof && secondProof < destroy) {
		t.Fatalf("cleanup ordering is unsafe:\n%s", script)
	}
	for _, property := range []string{
		replicationPropertyPolicyID,
		replicationPropertyRunID,
		replicationPropertyOwnerEpoch,
	} {
		if !strings.Contains(script, property) {
			t.Fatalf("cleanup proof omitted %s", property)
		}
	}
}

func TestParseZFSPropertyValuesRequiresLocalSource(t *testing.T) {
	values := parseZFSPropertyValues(strings.Join([]string{
		replicationPropertyPolicyID + "\t7\tlocal",
		replicationPropertyRunID + "\trun-1\tinherited from tank",
		replicationPropertyOwnerEpoch + "\t9\treceived",
	}, "\n"))
	if values[replicationPropertyPolicyID] != "7" {
		t.Fatalf("expected local property, got %#v", values)
	}
	if _, exists := values[replicationPropertyRunID]; exists {
		t.Fatalf("inherited provenance must not be accepted: %#v", values)
	}
	if _, exists := values[replicationPropertyOwnerEpoch]; exists {
		t.Fatalf("received provenance must not be accepted: %#v", values)
	}
}

func TestExactReplicationDatasetPathRejectsShellAndSnapshotSyntax(t *testing.T) {
	for _, invalid := range []string{
		"tank/guest/",
		"/tank/guest",
		"tank/guest@snap",
		"tank/guest$(id)",
		"tank//guest",
		"tank/../guest",
	} {
		if _, err := validateExactReplicationDatasetPath(invalid); err == nil {
			t.Fatalf("expected exact path validation failure for %q", invalid)
		}
	}
	if got, err := validateExactReplicationDatasetPath("tank/sylve/virtual-machines/10"); err != nil || got != "tank/sylve/virtual-machines/10" {
		t.Fatalf("valid exact path rejected: got=%q err=%v", got, err)
	}
}

func TestParseAndSplitPreviousReplicationRetention(t *testing.T) {
	output := strings.Join([]string{
		"tank/guest\t300",
		"tank/guest_previous-old\t100",
		"tank/guest_previous-new\t200",
		"tank/guest_previous-new/child\t201",
		"tank/other_previous-x\t400",
	}, "\n")
	datasets, err := parsePreviousReplicationDatasets(output, "tank/guest")
	if err != nil {
		t.Fatalf("parse previous generations: %v", err)
	}
	if len(datasets) != 2 || datasets[0].Name != "tank/guest_previous-new" || datasets[1].Name != "tank/guest_previous-old" {
		t.Fatalf("unexpected previous generations: %#v", datasets)
	}
	kept, removable := splitPreviousReplicationRetention(datasets, 1)
	if len(kept) != 1 || kept[0].Name != "tank/guest_previous-new" {
		t.Fatalf("unexpected kept generations: %#v", kept)
	}
	if len(removable) != 1 || removable[0].Name != "tank/guest_previous-old" {
		t.Fatalf("unexpected removable generations: %#v", removable)
	}
}

func TestPreviousReplicationProofRequiresLocalPolicyRoleAndTarget(t *testing.T) {
	properties := map[string]string{
		replicationPropertyPolicyID: "7",
		replicationPropertyRole:     replicationRoleStandby,
		replicationPropertyTarget:   "tank/guest",
	}
	if !isLocallyProvenPreviousReplicationDataset(properties, 7, "tank/guest") {
		t.Fatal("complete local proof was rejected")
	}
	delete(properties, replicationPropertyTarget)
	if isLocallyProvenPreviousReplicationDataset(properties, 7, "tank/guest") {
		t.Fatal("missing exact target provenance must be rejected")
	}
}

func TestDestroyPreviousReplicationScriptGuardsBeforeDestroy(t *testing.T) {
	script := buildDestroyProvenPreviousReplicationScript(
		"tank/guest_previous-old",
		"tank/guest",
		7,
	)
	guardIndex := strings.Index(script, "replication_previous_policy_not_local")
	destroyIndex := strings.Index(script, "zfs destroy -r")
	if guardIndex < 0 || destroyIndex < 0 || guardIndex > destroyIndex {
		t.Fatalf("expected local provenance guards before destroy:\n%s", script)
	}
	for _, expectedText := range []string{
		"replication_previous_role_not_local",
		"replication_previous_target_not_local",
		"replication_previous_not_readonly",
		"expected_policy=\"7\"",
	} {
		if !strings.Contains(script, expectedText) {
			t.Fatalf("expected %q in cleanup script:\n%s", expectedText, script)
		}
	}
	requireValidShellScript(t, script)
}

func TestReplicationZFSSendArgsUseProvenIncrementalBase(t *testing.T) {
	args := replicationZFSSendArgs("tank/source", "ha_next", "ha_base", true)
	joined := strings.Join(args, " ")
	for _, expected := range []string{
		"send --raw -P -R",
		"-i @ha_base",
		"tank/source@ha_next",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected %q in encrypted incremental send args: %q", expected, joined)
		}
	}
	if strings.Contains(joined, " -L ") || strings.Contains(joined, " -c ") {
		t.Fatalf("encrypted replication must remain raw: %q", joined)
	}

	fullArgs := strings.Join(replicationZFSSendArgs("tank/source", "ha_first", "", false), " ")
	if strings.Contains(fullArgs, " -i ") {
		t.Fatalf("an unproven baseline must produce a full send: %q", fullArgs)
	}
	if !strings.Contains(fullArgs, "send -P -R -L -c -e tank/source@ha_first") {
		t.Fatalf("unexpected unencrypted full send args: %q", fullArgs)
	}
}

func TestSeedReplicationStagingScriptValidatesBeforeCopyAndSupportsRaw(t *testing.T) {
	opts := ReplicationZFSTransferOptions{
		PolicyID:     7,
		RunID:        "run-next",
		OwnerEpoch:   9,
		SnapshotName: "ha_next",
		SnapshotGUID: "200",
	}
	expectedCurrent := replicationCurrentStandbySeedProperties(7, "tank/source", "tank/target")
	receiveProperties := replicationProvenanceProperties(
		opts,
		"tank/source",
		"tank/target",
		replicationStateReceiving,
	)
	script := buildSeedReplicationStagingScript(
		"tank/target",
		"tank/target_gen-run-next",
		"ha_base",
		"100",
		expectedCurrent,
		receiveProperties,
	)

	sendIndex := strings.Index(script, "zfs send --raw")
	if sendIndex < 0 {
		t.Fatalf("encrypted target-local seed must use a raw recursive send:\n%s", script)
	}
	for _, guard := range []string{
		"replication_staging_seed_path_exists",
		"provenance_not_local:" + replicationPropertyPolicyID,
		"provenance_mismatch:" + replicationPropertySource,
		"provenance_mismatch:" + replicationPropertyTarget,
		"provenance_mismatch:" + replicationPropertyRole,
		"provenance_mismatch:" + replicationPropertyState,
		"current_not_recursively_readonly",
		"common_snapshot_guid_mismatch",
	} {
		guardIndex := strings.Index(script, guard)
		if guardIndex < 0 || guardIndex > sendIndex {
			t.Fatalf("expected guard %q before target-local send:\n%s", guard, script)
		}
	}
	for _, expected := range []string{
		"zfs send --raw -P -R \"$current@$snap\"",
		"zfs send -P -R -L -c -e \"$current@$snap\"",
		"zfs recv -u -x mountpoint -o canmount=noauto -o readonly=on",
		"replication_staging_seed_encryption_detection_failed",
		replicationPropertyRunID + "=run-next",
		replicationPropertyState + "=" + replicationStateReceiving,
		"replication_staging_seed_snapshot_guid_mismatch",
		"replication_staging_seeded:$snap:$expected_guid",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("expected %q in target-local seed script:\n%s", expected, script)
		}
	}
	if strings.Contains(script, "zfs destroy") || strings.Contains(script, "zfs recv -F") {
		t.Fatalf("the seed primitive must not destroy or force-receive datasets:\n%s", script)
	}
	requireValidShellScript(t, script)
}

func TestCurrentStandbySeedProofIsExactAndMinimal(t *testing.T) {
	properties := replicationCurrentStandbySeedProperties(7, "tank/source", "tank/target")
	expected := map[string]string{
		replicationPropertyPolicyID: "7",
		replicationPropertySource:   "tank/source",
		replicationPropertyTarget:   "tank/target",
		replicationPropertyRole:     replicationRoleStandby,
		replicationPropertyState:    replicationStateReady,
	}
	if err := verifyReplicationPropertyValues(properties, expected); err != nil {
		t.Fatalf("complete current-standby proof rejected: %v", err)
	}
	for _, runSpecific := range []string{
		replicationPropertyRunID,
		replicationPropertyOwnerEpoch,
		replicationPropertySnapshot,
		replicationPropertySnapshotGUID,
	} {
		if _, exists := properties[runSpecific]; exists {
			t.Fatalf("baseline proof must permit a prior run while preserving lineage; unexpected %s", runSpecific)
		}
	}
}

func TestReplicationDatasetMissingResultDoesNotMaskTransportErrors(t *testing.T) {
	if !replicationDatasetMissingResult("cannot open 'tank/missing': dataset does not exist", errors.New("exit status 1")) {
		t.Fatal("a missing fresh staging dataset must be treated as absent")
	}
	if replicationDatasetMissingResult("", errors.New("ssh: connection timed out")) {
		t.Fatal("transport failures must not be treated as an absent staging dataset")
	}
	if replicationDatasetMissingResult("", errors.New("transport wrapper: dataset does not exist")) {
		t.Fatal("missing-like text outside captured ZFS output must not be treated as absence")
	}
}

func TestReplicationDatasetListedExactly(t *testing.T) {
	if !replicationDatasetListedExactly("warning line\ntank/backups\n", "tank/backups") {
		t.Fatal("an exact dataset-name line should be recognized")
	}
	if replicationDatasetListedExactly("tank/backups-old\n", "tank/backups") {
		t.Fatal("a different non-empty dataset line must not prove existence")
	}
}

func TestVerifyReplicationReadonlyValuesFailsClosed(t *testing.T) {
	if err := verifyReplicationReadonlyValues("on\non\n"); err != nil {
		t.Fatalf("valid recursive readonly output rejected: %v", err)
	}
	if err := verifyReplicationReadonlyValues(""); err == nil {
		t.Fatal("empty readonly output must not prove fencing")
	}
	if err := verifyReplicationReadonlyValues("on\noff\n"); err == nil {
		t.Fatal("one writable descendant must fail recursive fencing proof")
	}
}

func TestLocalSnapshotMissingResultDoesNotMaskExecutionErrors(t *testing.T) {
	if !localSnapshotMissingResult("cannot destroy 'tank/source@ha_failed': snapshot does not exist", errors.New("exit status 1")) {
		t.Fatal("an already absent snapshot should make cleanup idempotent")
	}
	if localSnapshotMissingResult("", errors.New("exec: zfs: no such file or directory")) {
		t.Fatal("an unavailable zfs executable must remain a cleanup failure")
	}
}

func TestShouldCleanupReplicationSourceSnapshot(t *testing.T) {
	ordinaryFailure := errors.New("transfer failed")
	tests := []struct {
		name           string
		createdHere    bool
		cleanupProven  bool
		targetVerified bool
		runErr         error
		want           bool
	}{
		{
			name:          "certain failure created here",
			createdHere:   true,
			cleanupProven: true,
			runErr:        ordinaryFailure,
			want:          true,
		},
		{
			name:          "successful run",
			createdHere:   true,
			cleanupProven: true,
		},
		{
			name:          "replayed snapshot",
			cleanupProven: true,
			runErr:        ordinaryFailure,
		},
		{
			name:          "commit uncertain",
			createdHere:   true,
			cleanupProven: true,
			runErr:        errReplicationGenerationCommitUncertain,
		},
		{
			name:           "target generation verified",
			createdHere:    true,
			cleanupProven:  true,
			targetVerified: true,
			runErr:         ordinaryFailure,
		},
		{
			name:        "staging cleanup unproven",
			createdHere: true,
			runErr:      ordinaryFailure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldCleanupReplicationSourceSnapshot(
				tt.createdHere,
				tt.cleanupProven,
				tt.targetVerified,
				tt.runErr,
			)
			if got != tt.want {
				t.Fatalf("shouldCleanupReplicationSourceSnapshot()=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecursiveEncryptionDetectionRequiresRawForAnyDescendant(t *testing.T) {
	raw, err := replicationEncryptionValuesRequireRaw("off\noff\naes-256-gcm\n")
	if err != nil || !raw {
		t.Fatalf("an encrypted descendant must force a raw recursive send: raw=%v err=%v", raw, err)
	}
	raw, err = replicationEncryptionValuesRequireRaw("off\n-\noff\n")
	if err != nil || raw {
		t.Fatalf("an entirely unencrypted hierarchy should use the compressed send path: raw=%v err=%v", raw, err)
	}
	if _, err := replicationEncryptionValuesRequireRaw("\n\t\n"); err == nil {
		t.Fatal("missing encryption metadata must fail closed")
	}
}

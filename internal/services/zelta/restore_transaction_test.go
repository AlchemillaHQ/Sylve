// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestRestoreStagingReceiveOptions(t *testing.T) {
	jobID := uint(42)
	identity := newRestoreStagingIdentity(&jobID, 7, "tank/live")
	options, err := identity.receiveTopOptions()
	if err != nil {
		t.Fatalf("receiveTopOptions: %v", err)
	}
	for _, expected := range []string{
		restorePropertyRole + "=" + restoreRoleStaging,
		restorePropertyOwner + "=job-42",
		restorePropertyDestination + "=tank/live",
		restorePropertyAttempt + "=" + identity.Attempt,
	} {
		if !strings.Contains(options, expected) {
			t.Fatalf("receive options missing %q: %s", expected, options)
		}
	}
}

func TestPrepareRestoreStagingDatasetRequiresLocalOwnershipRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	service := &Service{GZFS: client}
	jobID := uint(43)
	destination := pool + "/live"
	staging := destination + ".restoring"
	identity := newRestoreStagingIdentity(&jobID, 0, destination)
	previous := identity
	previous.Attempt = "previous-attempt"

	zfstest.EnsureDataset(t, client, staging+"/child")
	setRestoreStagingPropertiesForTest(t, staging, previous)
	if err := service.prepareRestoreStagingDataset(t.Context(), staging, identity); err != nil {
		t.Fatalf("cleanup owned staging: %v", err)
	}
	if exists, err := service.localDatasetExists(t.Context(), staging); err != nil || exists {
		t.Fatalf("owned staging exists=%v err=%v", exists, err)
	}

	parent := pool + "/foreign"
	inheritedStaging := parent + "/live.restoring"
	zfstest.EnsureDataset(t, client, parent)
	setRestoreStagingPropertiesForTest(t, parent, previous)
	zfstest.EnsureDataset(t, client, inheritedStaging)
	inheritedIdentity := identity
	inheritedIdentity.Destination = parent + "/live"
	if err := service.prepareRestoreStagingDataset(t.Context(), inheritedStaging, inheritedIdentity); err == nil ||
		!strings.Contains(err.Error(), "requires_manual_cleanup") {
		t.Fatalf("inherited provenance was accepted: %v", err)
	}
	if exists, err := service.localDatasetExists(t.Context(), inheritedStaging); err != nil || !exists {
		t.Fatalf("foreign staging exists=%v err=%v", exists, err)
	}
}

func TestPrepareRestoreStagingDatasetPreservesDependentCloneRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	service := &Service{GZFS: client}
	jobID := uint(44)
	destination := pool + "/live"
	staging := destination + ".restoring"
	clone := pool + "/dependent-clone"
	identity := newRestoreStagingIdentity(&jobID, 0, destination)
	identity.Attempt = "owned-attempt"

	zfstest.EnsureDataset(t, client, staging)
	setRestoreStagingPropertiesForTest(t, staging, identity)
	for _, args := range [][]string{
		{"snapshot", staging + "@preserve"},
		{"clone", staging + "@preserve", clone},
	} {
		if output, err := exec.Command("zfs", args...).CombinedOutput(); err != nil {
			t.Fatalf("zfs %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}

	retryIdentity := identity
	retryIdentity.Attempt = "next-attempt"
	err := service.prepareRestoreStagingDataset(t.Context(), staging, retryIdentity)
	if err == nil || !strings.Contains(err.Error(), "restore_owned_staging_cleanup_failed") {
		t.Fatalf("dependent clone did not block cleanup: %v", err)
	}
	for _, dataset := range []string{staging, clone} {
		if exists, checkErr := service.localDatasetExists(t.Context(), dataset); checkErr != nil || !exists {
			t.Fatalf("dataset %s exists=%v err=%v", dataset, exists, checkErr)
		}
	}
}

func TestRollbackRestorePromotionAfterActivationFailureRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	service := &Service{GZFS: client}
	destination := pool + "/live"
	staging := destination + ".restoring"
	zfstest.EnsureDataset(t, client, destination+"/old-child")
	zfstest.EnsureDataset(t, client, staging+"/new-child")
	oldGUID := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", destination))
	if mounted := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "mounted", destination)); mounted != "yes" {
		t.Fatalf("destination mounted before rollback test = %q, want yes", mounted)
	}

	backupDataset, err := service.promoteRestoredDataset(t.Context(), staging, destination)
	if err != nil {
		t.Fatalf("promote restore candidate: %v", err)
	}
	activationErr := errors.New("deterministic_activation_failure")
	err = service.rollbackRestorePromotionAfterError(destination, backupDataset, true, activationErr)
	if !errors.Is(err, activationErr) || strings.Contains(err.Error(), "rollback_failed") {
		t.Fatalf("rollback result: %v", err)
	}

	if got := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", destination)); got != oldGUID {
		t.Fatalf("destination GUID after rollback = %q, want %q", got, oldGUID)
	}
	if mounted := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "mounted", destination)); mounted != "yes" {
		t.Fatalf("destination mounted after rollback = %q, want yes", mounted)
	}
	for dataset, want := range map[string]bool{
		destination + "/old-child": true,
		destination + "/new-child": false,
		backupDataset:              false,
	} {
		exists, checkErr := service.localDatasetExists(t.Context(), dataset)
		if checkErr != nil || exists != want {
			t.Fatalf("dataset %s exists=%v want=%v err=%v", dataset, exists, want, checkErr)
		}
	}
}

func TestRollbackRestoredDatasetBackupsRemovesNewRootRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping ZFS restore rollback integration in short mode")
	}
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	newRoot := pool + "/new-root"
	existingRoot := pool + "/existing-root"
	staging := pool + "/existing-root.restoring"
	zfstest.EnsureDataset(t, client, newRoot)
	zfstest.EnsureDataset(t, client, existingRoot+"/old-child")
	zfstest.EnsureDataset(t, client, staging+"/new-child")
	oldGUID := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", existingRoot))

	service := &Service{GZFS: client}
	backup, err := service.promoteRestoredDataset(t.Context(), staging, existingRoot)
	if err != nil {
		t.Fatalf("promote existing root: %v", err)
	}
	if err := service.rollbackRestoredDatasetBackups([]restoredDatasetBackup{
		{destination: newRoot},
		{destination: existingRoot, backup: backup},
	}); err != nil {
		t.Fatalf("rollback restored roots: %v", err)
	}
	if exists, err := service.localDatasetExists(t.Context(), newRoot); err != nil || exists {
		t.Fatalf("new root remains after rollback: exists=%v err=%v", exists, err)
	}
	if got := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", existingRoot)); got != oldGUID {
		t.Fatalf("existing root GUID after rollback = %q, want %q", got, oldGUID)
	}
}

func TestRollbackPromotedDatasetMissingArchivePreservesDestinationRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping ZFS restore rollback integration in short mode")
	}
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	destination := pool + "/live"
	missingArchive := pool + "/missing-archive"
	zfstest.EnsureDataset(t, client, destination+"/only-copy")
	service := &Service{GZFS: client}
	if err := service.rollbackPromotedDataset(t.Context(), destination, missingArchive); err == nil || !strings.Contains(err.Error(), "restore_backup_dataset_missing") {
		t.Fatalf("missing archive rollback error = %v", err)
	}
	for _, dataset := range []string{destination, destination + "/only-copy"} {
		if exists, err := service.localDatasetExists(t.Context(), dataset); err != nil || !exists {
			t.Fatalf("dataset %s was removed despite missing archive: exists=%v err=%v", dataset, exists, err)
		}
	}
}

func TestActivateTargetGenerationsRollsBackEarlierSwapRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping remote restore generation integration in short mode")
	}
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	sshHost, sshKeyPath := requireRestoreLocalhostSSH(t)

	backupRoot := pool + "/backup"
	activeA := backupRoot + "/a"
	selectedA := activeA + "_gen-old"
	activeB := backupRoot + "/b"
	missingB := activeB + "_gen-missing"
	for _, dataset := range []string{activeA, selectedA, activeB} {
		zfstest.EnsureDataset(t, client, dataset)
	}
	originalActiveGUID := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", activeA))
	originalSelectedGUID := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", selectedA))

	service := &Service{GZFS: client}
	target := &clusterModels.BackupTarget{
		SSHHost:    sshHost,
		SSHKeyPath: sshKeyPath,
		BackupRoot: backupRoot,
		Enabled:    true,
	}
	_, err := service.activateTargetGenerationsForRestore(
		t.Context(),
		target,
		[]restoreTargetGenerationSelection{
			{ActiveDataset: activeA, SelectedDataset: selectedA},
			{ActiveDataset: activeB, SelectedDataset: missingB},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "selected_restore_dataset_not_found") {
		t.Fatalf("expected second activation failure, got %v", err)
	}
	if got := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", activeA)); got != originalActiveGUID {
		t.Fatalf("active generation GUID after rollback = %q, want %q", got, originalActiveGUID)
	}
	if got := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", selectedA)); got != originalSelectedGUID {
		t.Fatalf("selected generation GUID after rollback = %q, want %q", got, originalSelectedGUID)
	}

	activeC := backupRoot + "/c"
	selectedC := activeC + "_gen-old"
	activeD := backupRoot + "/d"
	missingD := activeD + "_gen-missing"
	for _, dataset := range []string{selectedC, activeD} {
		zfstest.EnsureDataset(t, client, dataset)
	}
	originalSelectedCGUID := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", selectedC))
	_, err = service.activateTargetGenerationsForRestore(
		t.Context(),
		target,
		[]restoreTargetGenerationSelection{
			{ActiveDataset: activeC, SelectedDataset: selectedC},
			{ActiveDataset: activeD, SelectedDataset: missingD},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "selected_restore_dataset_not_found") {
		t.Fatalf("expected activation failure after absent-active swap, got %v", err)
	}
	if exists, checkErr := service.localDatasetExists(t.Context(), activeC); checkErr != nil || exists {
		t.Fatalf("new active path remains after rollback: exists=%v err=%v", exists, checkErr)
	}
	if got := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", selectedC)); got != originalSelectedCGUID {
		t.Fatalf("selected generation C GUID after rollback = %q, want %q", got, originalSelectedCGUID)
	}
}

func TestRollbackTargetGenerationResumesPartialSwapRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping remote restore generation integration in short mode")
	}
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	sshHost, sshKeyPath := requireRestoreLocalhostSSH(t)

	backupRoot := pool + "/backup"
	active := backupRoot + "/data"
	selected := active + "_gen-old"
	archived := active + "_gen-newer"
	for _, dataset := range []string{selected, archived} {
		zfstest.EnsureDataset(t, client, dataset)
	}
	selectedGUID := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", selected))
	archivedGUID := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", archived))

	service := &Service{GZFS: client}
	target := &clusterModels.BackupTarget{
		SSHHost:    sshHost,
		SSHKeyPath: sshKeyPath,
		BackupRoot: backupRoot,
		Enabled:    true,
	}
	activation := restoreTargetGenerationActivation{
		ActiveDataset:   active,
		SelectedDataset: selected,
		ArchivedDataset: archived,
		ActiveExisted:   true,
		Activated:       true,
	}
	if err := service.rollbackTargetGenerationForRestore(t.Context(), target, activation); err != nil {
		t.Fatalf("resume partial remote rollback: %v", err)
	}
	if got := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", active)); got != archivedGUID {
		t.Fatalf("active GUID after resumed rollback = %q, want %q", got, archivedGUID)
	}
	if got := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", selected)); got != selectedGUID {
		t.Fatalf("selected GUID after resumed rollback = %q, want %q", got, selectedGUID)
	}
	if exists, err := service.localDatasetExists(t.Context(), archived); err != nil || exists {
		t.Fatalf("archive remains after resumed rollback: exists=%v err=%v", exists, err)
	}
	if err := service.rollbackTargetGenerationForRestore(t.Context(), target, activation); err != nil {
		t.Fatalf("idempotent remote rollback: %v", err)
	}
}

func TestScheduledEncryptedRestoreActivationFailureRollsBackRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping encrypted restore rollback integration in short mode")
	}
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	sshHost, sshKeyPath := requireRestoreLocalhostSSH(t)

	backupRoot := pool + "/backup"
	remoteDataset := backupRoot + "/encrypted"
	destination := pool + "/live"
	zfstest.EnsureDataset(t, client, backupRoot)
	zfstest.EnsureDataset(t, client, destination+"/old-child")
	passphrase := "restore-transaction-test-passphrase"
	create := exec.Command(
		"zfs", "create",
		"-o", "encryption=on",
		"-o", "keyformat=passphrase",
		"-o", "keylocation=prompt",
		remoteDataset,
	)
	create.Stdin = strings.NewReader(passphrase + "\n" + passphrase + "\n")
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create encrypted remote dataset: %v\n%s", err, output)
	}
	mustRunRestoreZFSTestCommand(t, "snapshot", remoteDataset+"@selected")
	oldGUID := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", destination))

	extractZeltaToTemp(t)
	database := testutil.NewSQLiteTestDB(t, &clusterModels.BackupEvent{})
	service := &Service{DB: database, GZFS: client}
	target := &clusterModels.BackupTarget{
		ID:         57,
		SSHHost:    sshHost,
		SSHKeyPath: sshKeyPath,
		BackupRoot: backupRoot,
		Enabled:    true,
	}
	jobID := uint(58)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_, err := service.runRestoreFromTargetSingleDataset(
		ctx,
		target,
		restoreFromTargetPayload{
			RemoteDataset:      remoteDataset,
			Snapshot:           "@selected",
			DestinationDataset: destination,
		},
		&jobID,
		false,
		true,
		false,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "restore_encryption_key_required") {
		t.Fatalf("expected restore activation failure, got %v", err)
	}
	if strings.Contains(err.Error(), "restore_rollback_failed") {
		t.Fatalf("restore rollback failed: %v", err)
	}
	if got := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "get", "-H", "-p", "-o", "value", "guid", destination)); got != oldGUID {
		t.Fatalf("destination GUID after failed restore = %q, want %q", got, oldGUID)
	}
	allDatasets := mustRunRestoreZFSTestCommand(t, "list", "-H", "-r", "-t", "filesystem,volume", "-o", "name", pool)
	if strings.Contains(allDatasets, destination+".restoring") || strings.Contains(allDatasets, destination+"_restore-backup-") {
		t.Fatalf("restore artifacts remain after rollback:\n%s", allDatasets)
	}
}

func setRestoreStagingPropertiesForTest(
	t *testing.T,
	dataset string,
	identity restoreStagingIdentity,
) {
	t.Helper()
	properties := identity.expectedProperties(true)
	for _, property := range restoreStagingPropertyNames {
		value := properties[property]
		if output, err := exec.Command("zfs", "set", property+"="+value, dataset).CombinedOutput(); err != nil {
			t.Fatalf("set %s on %s: %v\n%s", property, dataset, err, output)
		}
	}
}

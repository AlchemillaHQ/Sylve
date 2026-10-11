// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"gorm.io/gorm"
)

const testRecoveryPassphrase = "isolated-recovery-passphrase"

// Commands, trust state and every OS path are isolated; no host service is
// stopped and no real controller or Samba database is contacted by these tests.
func newRecoveryTestService(t *testing.T) (*Service, map[string]string) {
	t.Helper()
	s := newTestService(t)
	if err := s.DB.AutoMigrate(&sambaModels.SambaShare{}); err != nil {
		t.Fatal(err)
	}
	input := testADInput()
	username, password := "joinuser", "isolated-enrollment-password"
	input.JoinUsername, input.JoinPassword = &username, &password
	ad, err := s.SaveAD(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	ad.Joined, ad.Enabled = true, true
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	data := nativeKeytab(t, 1)
	ciphertext, err := s.encryptKeytab(data)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := parseKeytab(data)
	if err := s.DB.Create(&directoryModels.KerberosKeytab{Name: MachineKeytabName, Managed: true, Principals: keytabPrincipals(entries), Ciphertext: ciphertext}).Error; err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.machineKeytabPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.writeKerberos(s.DB); err != nil {
		t.Fatal(err)
	}
	share := sambaModels.SambaShare{Name: "recovery-share", Dataset: "isolated-guid", DirectoryPermissions: directoryModels.SharePermissions{Read: []directoryModels.Principal{{SID: "S-1-5-21-1-2-3-1100", Name: "EXAMPLE\\alice", Kind: "user", UnixID: 1001100}}}}
	if err := s.DB.Create(&share).Error; err != nil {
		t.Fatal(err)
	}
	// Disabled shares still protect their saved identities.
	if err := s.DB.Model(&share).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Exec("CREATE TABLE unrelated_recovery_test (value TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Exec("INSERT INTO unrelated_recovery_test VALUES ('before backup')").Error; err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, dir := range []string{"private", "state", "cache"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	healthy := healthyCommands(t, s, "")
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if path == "/usr/local/bin/testparm" && slices.Contains(args, "-v") {
			return "[global]\nprivate dir = " + filepath.Join(root, "private") + "\nstate directory = " + filepath.Join(root, "state") + "\ncache directory = " + filepath.Join(root, "cache") + "\n", nil
		}
		if path == "/usr/local/bin/tdbbackup" {
			if len(args) != 2 || args[0] != "-v" || !strings.HasPrefix(args[1], filepath.Join(s.dataPath, "directory-services", "restore-")) {
				t.Fatal("TDB verification escaped the recovery staging directory")
			}
			return "", nil
		}
		if path == "/usr/local/bin/net" && slices.Contains(args, "testjoin") {
			if !slices.Contains(args, "--option=kerberos method=secrets only") || !slices.Contains(args, "--machine-pass") {
				t.Fatal("recovery did not verify the staged machine secret")
			}
			for _, option := range []string{"private dir", "state directory", "cache directory", "lock directory", "pid directory"} {
				found := false
				for _, arg := range args {
					if value, ok := strings.CutPrefix(arg, "--option="+option+"="); ok && strings.HasPrefix(value, filepath.Join(s.dataPath, "directory-services", "restore-")) {
						found = true
					}
				}
				if !found {
					t.Fatal("machine trust verification used a live Samba directory")
				}
			}
			return "OK", nil
		}
		return healthy(ctx, path, args, input, env)
	}
	s.checkSambaStopped = func(context.Context) error { return nil }
	paths, err := s.recoveryPaths(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"secrets.tdb", "winbindd_idmap.tdb", "group_mapping.tdb", "winbindd_cache.tdb", "gencache.tdb"} {
		if err := os.WriteFile(paths[name], []byte("isolated-"+name+"-state"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s.WriteSamba = func(context.Context, bool) error {
		t.Fatal("recovery regenerated shares or restarted Samba")
		return nil
	}
	return s, paths
}

func exportTestRecovery(t *testing.T, s *Service) RecoveryInput {
	t.Helper()
	ad, err := s.GetAD()
	if err != nil {
		t.Fatal(err)
	}
	input := RecoveryInput{Passphrase: testRecoveryPassphrase, ConfirmComputerName: ad.ComputerName}
	input.File, err = s.ExportRecovery(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func recoverySnapshots(t *testing.T, paths map[string]string) map[string]fileSnapshot {
	t.Helper()
	files := map[string]fileSnapshot{}
	for name, path := range paths {
		file, err := snapshot(path)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = file
	}
	return files
}

func assertRecoveryUnchanged(t *testing.T, paths map[string]string, before map[string]fileSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(before, recoverySnapshots(t, paths)) {
		t.Fatal("rejected recovery changed OS state")
	}
}

func TestRecoveryExportEncryptsAndCapturesCoordinatedState(t *testing.T) {
	s, paths := newRecoveryTestService(t)
	before := recoverySnapshots(t, paths)
	input := exportTestRecovery(t, s)
	for _, secret := range [][]byte{[]byte(testRecoveryPassphrase), []byte("isolated-enrollment-password"), before["secrets.tdb"].data, before["machine-keytab"].data, before["encryption-key"].data} {
		if bytes.Contains(input.File, secret) {
			t.Fatal("recovery export contains unencrypted credentials")
		}
	}
	bundle, err := openRecovery(input.File, input.Passphrase)
	if err != nil || bundle.SambaVersion != "Version 4.23.11" || len(bundle.Files) != len(paths) {
		t.Fatal("encrypted bundle is incomplete")
	}
	for name, file := range before {
		captured := bundle.Files[name]
		if captured.Exists != file.exists || !bytes.Equal(captured.Data, file.data) {
			t.Fatal("recovery bundle omitted coordinated state")
		}
	}
	stage := t.TempDir()
	db, err := openRecoveryDatabase(stage, bundle.Database)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	state, err := readRecoveryState(db)
	if err != nil || len(state.AD.JoinPasswordCiphertext) == 0 || len(state.Keytabs) != 1 || len(state.IDs) != 1 {
		t.Fatal("database snapshot omitted encrypted credentials or disabled-share identities")
	}
	assertRecoveryUnchanged(t, paths, before)
	if s.RecoveryPending() {
		t.Fatal("export left a restore journal")
	}
}

func TestRecoveryEncryptionRejectsWrongPassphraseTamperingAndWrongNode(t *testing.T) {
	s, _ := newRecoveryTestService(t)
	input := exportTestRecovery(t, s)
	if _, err := openRecovery(input.File, "incorrect-passphrase"); !errors.Is(err, ErrInvalid) {
		t.Fatal("wrong recovery passphrase accepted")
	}
	tampered := slices.Clone(input.File)
	tampered[len(tampered)-1] ^= 1
	if _, err := openRecovery(tampered, input.Passphrase); !errors.Is(err, ErrInvalid) {
		t.Fatal("tampered recovery file accepted")
	}
	bundle, err := openRecovery(input.File, input.Passphrase)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Hostname = "different-recovery-node.invalid"
	file, err := sealRecovery(bundle, input.Passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openRecovery(file, input.Passphrase); !errors.Is(err, ErrConflict) {
		t.Fatal("another node's recovery file accepted")
	}
	for _, data := range [][]byte{nil, []byte(recoveryMagic), bytes.Repeat([]byte{0}, 52)} {
		if _, err := openRecovery(data, input.Passphrase); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid recovery format accepted")
		}
	}
}

func TestRecoveryRestorePreservesUnrelatedStateAndCapturedRotatedKeytab(t *testing.T) {
	s, paths := newRecoveryTestService(t)
	// Winbind has captured a newer key before the DB monitor runs.
	rotated := nativeKeytab(t, 2)
	if err := os.WriteFile(s.machineKeytabPath, rotated, 0600); err != nil {
		t.Fatal(err)
	}
	input := exportTestRecovery(t, s)
	if err := s.DB.Exec("UPDATE unrelated_recovery_test SET value = 'after backup'").Error; err != nil {
		t.Fatal(err)
	}
	nssInfo, err := os.Stat(s.nssPath)
	if err != nil {
		t.Fatal(err)
	}
	nssBefore := readTestFile(t, s.nssPath)
	for _, name := range []string{"machine-keytab", "encryption-key", "secrets.tdb", "winbindd_idmap.tdb"} {
		if err := os.Remove(paths[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RestoreRecovery(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if s.RecoveryPending() || !bytes.Equal(readTestFile(t, s.machineKeytabPath), rotated) {
		t.Fatal("successful recovery left pending state or stale machine keys")
	}
	for _, name := range []string{"machine-keytab", "encryption-key", "secrets.tdb", "winbindd_idmap.tdb", "system-keytab"} {
		info, err := os.Stat(paths[name])
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("restored sensitive state is not private")
		}
	}
	for _, name := range []string{"winbindd_cache.tdb", "gencache.tdb"} {
		if _, err := os.Stat(paths[name]); !os.IsNotExist(err) {
			t.Fatal("recovery restored stale Samba caches")
		}
	}
	nssAfter, _ := os.Stat(s.nssPath)
	if !os.SameFile(nssInfo, nssAfter) || !bytes.Equal(nssBefore, readTestFile(t, s.nssPath)) {
		t.Fatal("recovery rewrote NSS")
	}
	var unrelated string
	if err := s.DB.Raw("SELECT value FROM unrelated_recovery_test").Scan(&unrelated).Error; err != nil || unrelated != "after backup" {
		t.Fatal("recovery replaced unrelated application state")
	}
	var settings models.BasicSettings
	if err := s.DB.First(&settings).Error; err != nil || !slices.Contains(settings.Services, models.SambaServer) {
		t.Fatal("recovery changed the service-enabled setting")
	}
	var keytab directoryModels.KerberosKeytab
	if err := s.DB.Where("managed = ?", true).First(&keytab).Error; err != nil {
		t.Fatal(err)
	}
	plain, err := s.decryptKeytab(keytab.Ciphertext)
	if err != nil || !bytes.Equal(plain, rotated) {
		t.Fatal("recovery published an older DB machine keytab")
	}
	clear(plain)
	ad, _ := s.GetAD()
	password, err := s.decryptSecret(ad.JoinPasswordCiphertext, joinPasswordPurpose(ad))
	if err != nil || string(password) != "isolated-enrollment-password" {
		t.Fatal("saved enrollment credentials did not survive recovery")
	}
	clear(password)
}

func TestRecoveryRejectsChangedMembershipAllocationsCredentialsAndShares(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *Service, map[string]string)
	}{
		{"domain", func(t *testing.T, s *Service, _ map[string]string) {
			if err := s.DB.Model(&directoryModels.ActiveDirectory{}).Where("id = 1").Update("domain", "OTHER.COM").Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"primary mapping", func(t *testing.T, s *Service, _ map[string]string) {
			if err := s.DB.Model(&directoryModels.ActiveDirectory{}).Where("id = 1").Update("range_low", 2000000).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"newer machine key", func(t *testing.T, s *Service, _ map[string]string) {
			if err := os.WriteFile(s.machineKeytabPath, nativeKeytab(t, 2), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"different encryption key", func(t *testing.T, _ *Service, paths map[string]string) {
			if err := os.WriteFile(paths["encryption-key"], bytes.Repeat([]byte{42}, 32), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"new allocations", func(t *testing.T, _ *Service, paths map[string]string) {
			if err := os.WriteFile(paths["winbindd_idmap.tdb"], []byte("newer allocations"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"new AUTORID allocations", func(t *testing.T, _ *Service, paths map[string]string) {
			if err := os.WriteFile(paths["autorid.tdb"], []byte("new domain allocation"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"disabled share identity", func(t *testing.T, s *Service, _ map[string]string) {
			var share sambaModels.SambaShare
			if err := s.DB.First(&share).Error; err != nil {
				t.Fatal(err)
			}
			share.DirectoryPermissions.Read[0].UnixID++
			if err := s.DB.Save(&share).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"changed enrollment credentials", func(t *testing.T, s *Service, _ map[string]string) {
			ad, _ := s.GetAD()
			ciphertext, err := s.encryptSecret([]byte("replacement-enrollment-password"), joinPasswordPurpose(ad))
			if err != nil {
				t.Fatal(err)
			}
			ad.JoinPasswordCiphertext = ciphertext
			if err := s.DB.Save(&ad).Error; err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, paths := newRecoveryTestService(t)
			input := exportTestRecovery(t, s)
			test.mutate(t, s, paths)
			before := recoverySnapshots(t, paths)
			if err := s.RestoreRecovery(context.Background(), input); !errors.Is(err, ErrConflict) {
				t.Fatalf("unsafe restore was not refused: %v", err)
			}
			assertRecoveryUnchanged(t, paths, before)
			if s.RecoveryPending() {
				t.Fatal("rejected recovery left a journal")
			}
		})
	}
}

func TestRecoveryRequiresStoppedServicesAndComputerConfirmation(t *testing.T) {
	s, paths := newRecoveryTestService(t)
	input := exportTestRecovery(t, s)
	before := recoverySnapshots(t, paths)
	s.checkSambaStopped = func(context.Context) error { return ErrConflict }
	if _, err := s.ExportRecovery(context.Background(), input); !errors.Is(err, ErrConflict) {
		t.Fatal("running services permitted backup")
	}
	if err := s.RestoreRecovery(context.Background(), input); !errors.Is(err, ErrConflict) {
		t.Fatal("running services permitted restore")
	}
	s.checkSambaStopped = func(context.Context) error { return nil }
	input.ConfirmComputerName = "WRONG"
	if _, err := s.ExportRecovery(context.Background(), input); !errors.Is(err, ErrConflict) {
		t.Fatal("backup ignored computer confirmation")
	}
	if err := s.RestoreRecovery(context.Background(), input); !errors.Is(err, ErrInvalid) {
		t.Fatal("restore ignored computer confirmation")
	}
	assertRecoveryUnchanged(t, paths, before)
}

func TestStoppedServiceDetectionFailsClosed(t *testing.T) {
	s := newTestService(t)
	codeOne := exec.Command("/usr/bin/false").Run()
	for _, test := range []struct {
		name     string
		pgrepErr error
		expected error
	}{
		{"stopped", codeOne, nil}, {"running", nil, ErrConflict}, {"unknown", os.ErrPermission, ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			s.run = func(_ context.Context, path string, args []string, _ string, _ []string) (string, error) {
				if path == "/sbin/sysctl" {
					return "0\n", nil
				}
				if path != "/usr/bin/pgrep" || !reflect.DeepEqual(args, []string{"-j", "0", "-x", "smbd|nmbd|winbindd|samba"}) {
					t.Fatal("service detection escaped node jail scope")
				}
				return "", test.pgrepErr
			}
			if err := s.requireStoppedSamba(context.Background()); !errors.Is(err, test.expected) {
				t.Fatalf("service detection: %v", err)
			}
		})
	}
}

func TestRecoveryFailedApplyRollsBackWithoutChangingUnrelatedState(t *testing.T) {
	s, paths := newRecoveryTestService(t)
	input := exportTestRecovery(t, s)
	before := recoverySnapshots(t, paths)
	previous, err := readRecoveryState(s.DB)
	if err != nil {
		t.Fatal(err)
	}
	failed := false
	if err := s.DB.Callback().Create().Before("gorm:create").Register("test:recovery-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "kerberos_keytabs" && !failed {
			failed = true
			tx.AddError(errors.New("isolated recovery apply failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreRecovery(context.Background(), input); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "previous state was restored") {
		t.Fatalf("failed apply: %v", err)
	}
	assertRecoveryUnchanged(t, paths, before)
	after, err := readRecoveryState(s.DB)
	if err != nil || !reflect.DeepEqual(previous, after) || s.RecoveryPending() {
		t.Fatal("failed apply did not restore the exact previous directory state")
	}
}

func TestPendingRecoveryBlocksChangesAndAllowsProtectedRollback(t *testing.T) {
	s, paths := newRecoveryTestService(t)
	input := exportTestRecovery(t, s)
	before := recoverySnapshots(t, paths)
	if err := s.DB.Callback().Create().Before("gorm:create").Register("test:recovery-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "kerberos_keytabs" {
			tx.AddError(errors.New("isolated persistent DB failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreRecovery(context.Background(), input); !errors.Is(err, ErrUnavailable) || !s.RecoveryPending() {
		t.Fatal("failed rollback did not preserve the recovery journal")
	}
	journal, _ := s.recoveryJournal()
	info, err := os.Stat(journal)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("pending recovery journal is not protected")
	}
	status, err := s.Status(context.Background(), true)
	if err != nil || status.State != "FAULTED" || !status.RecoveryPending {
		t.Fatal("pending recovery was not surfaced")
	}
	for _, operation := range []func() error{
		func() error { _, err := s.SaveAD(context.Background(), testADInput()); return err },
		func() error { return s.SetEnabled(context.Background(), true) },
		func() error { return s.Join(context.Background(), JoinInput{}) },
		func() error { return s.Leave(context.Background(), LeaveInput{LocalOnly: true}) },
		func() error { return s.Reconcile(context.Background()) },
		func() error {
			_, err := s.SaveRealm(context.Background(), 0, RealmInput{Realm: "OTHER.COM"})
			return err
		},
		func() error {
			unlock, err := s.LockConfiguration()
			if unlock != nil {
				unlock()
			}
			return err
		},
	} {
		if err := operation(); !errors.Is(err, ErrConflict) {
			t.Fatalf("pending recovery allowed a configuration change: %v", err)
		}
	}
	if err := s.DB.Callback().Create().Remove("test:recovery-failure"); err != nil {
		t.Fatal(err)
	}
	wrong := input
	wrong.Passphrase = "wrong-rollback-passphrase"
	if err := s.RollbackRecovery(context.Background(), wrong); !errors.Is(err, ErrInvalid) || !s.RecoveryPending() {
		t.Fatal("wrong rollback passphrase discarded the protected journal")
	}
	if err := s.RollbackRecovery(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if s.RecoveryPending() {
		t.Fatal("successful rollback left recovery pending")
	}
	assertRecoveryUnchanged(t, paths, before)
}

func TestRecoveryRefusesUnverifiedTrustBeforeApplyingState(t *testing.T) {
	s, paths := newRecoveryTestService(t)
	input := exportTestRecovery(t, s)
	before := recoverySnapshots(t, paths)
	run := s.run
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if slices.Contains(args, "testjoin") {
			return "", errors.New("saved trust is obsolete")
		}
		return run(ctx, path, args, input, env)
	}
	if err := s.RestoreRecovery(context.Background(), input); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "no recovery state was applied") {
		t.Fatalf("unverified trust: %v", err)
	}
	assertRecoveryUnchanged(t, paths, before)
	if s.RecoveryPending() {
		t.Fatal("unverified trust created a restore journal")
	}
}

func TestRecoverySymlinksAndUnknownArtifactEntriesAreRefused(t *testing.T) {
	s, paths := newRecoveryTestService(t)
	input := exportTestRecovery(t, s)
	bundle, err := openRecovery(input.File, input.Passphrase)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Files["outside-path"] = recoveryFile{Exists: true, Data: []byte("unexpected")}
	input.File, err = sealRecovery(bundle, input.Passphrase)
	if err != nil {
		t.Fatal(err)
	}
	before := recoverySnapshots(t, paths)
	if err := s.RestoreRecovery(context.Background(), input); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown artifact path was accepted")
	}
	assertRecoveryUnchanged(t, paths, before)
	if err := os.Remove(paths["secrets.tdb"]); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(target, []byte("keep this content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, paths["secrets.tdb"]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExportRecovery(context.Background(), RecoveryInput{Passphrase: input.Passphrase, ConfirmComputerName: "SYLVE"}); err == nil {
		t.Fatal("symlinked trust secret was exported")
	}
	if string(readTestFile(t, target)) != "keep this content" {
		t.Fatal("recovery followed a symlink")
	}
}

func TestRecoveryStatusDoesNotCreateOwnedFiles(t *testing.T) {
	s := newTestService(t)
	if s.RecoveryPending() {
		t.Fatal("fresh service reports a pending recovery")
	}
	if _, err := s.Status(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dataPath, "directory-services")); !os.IsNotExist(err) {
		t.Fatal("status check created a recovery directory")
	}
}

func TestRecoveryFailedKerberosRegenerationRestoresOriginalBackupAbsence(t *testing.T) {
	s, paths := newRecoveryTestService(t)
	input := exportTestRecovery(t, s)
	if err := os.Remove(paths["original-kerberos"]); err != nil {
		t.Fatal(err)
	}
	before := recoverySnapshots(t, paths)
	injected := false
	if err := s.DB.Callback().Query().After("gorm:query").Register("test:kerberos-regeneration-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "active_directories" && s.RecoveryPending() && !injected {
			injected = true
			// Fail decryption after writeKerberos has created a missing original
			// configuration backup. All paths remain inside this test's sandbox.
			if err := os.WriteFile(paths["encryption-key"], []byte("invalid-test-key"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreRecovery(context.Background(), input); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "previous state was restored") {
		t.Fatalf("Kerberos regeneration rollback: %v", err)
	}
	if !injected || s.RecoveryPending() {
		t.Fatal("Kerberos regeneration failure did not roll back")
	}
	assertRecoveryUnchanged(t, paths, before)
}

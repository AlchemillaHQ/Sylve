// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gorm.io/gorm"
)

type recoveryServiceTestRuntime struct {
	running            map[string]bool
	actions            []string
	stateErr           error
	stopErr            error
	startErr           error
	stopLeavesRunning  bool
	startLeavesStopped bool
	onAction           func(string)
}

func fakeRecoveryServiceRuntime(t *testing.T, s *Service, daemons ...string) *recoveryServiceTestRuntime {
	t.Helper()
	runtime := &recoveryServiceTestRuntime{running: map[string]bool{}}
	for _, daemon := range daemons {
		runtime.running[daemon] = true
	}
	notRunning := exec.Command("/usr/bin/false").Run()
	run := s.run
	s.checkSambaStopped = nil
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		switch path {
		case "/sbin/sysctl":
			if !slices.Equal(args, []string{"-n", "security.jail.jid"}) {
				t.Fatal("service state lookup escaped jail scope")
			}
			return "42\n", runtime.stateErr
		case "/usr/bin/pgrep":
			if len(args) != 4 || !slices.Equal(args[:3], []string{"-j", "42", "-x"}) {
				t.Fatal("service process lookup escaped node jail scope")
			}
			if runtime.stateErr != nil {
				return "", runtime.stateErr
			}
			for _, daemon := range strings.Split(args[3], "|") {
				if runtime.running[daemon] {
					return "4242\n", nil
				}
			}
			return "", notRunning
		case "/bin/sh":
			if len(args) != 7 || args[0] != "-c" || args[1] != recoveryServiceScript || args[2] != "sylve-directory-recovery" || args[5] != s.recoveryServicePath || args[6] != s.smbPath {
				t.Fatal("unexpected recovery service command")
			}
			action := args[3]
			runtime.actions = append(runtime.actions, action+" "+args[4])
			if action == "onestart" && s.RecoveryPending() {
				t.Fatal("recovery attempted to start services with a pending journal")
			}
			if runtime.onAction != nil {
				runtime.onAction(action)
			}
			for index, daemon := range strings.Fields(args[4]) {
				if !slices.Contains([]string{"nmbd", "smbd", "winbindd"}, daemon) {
					t.Fatal("recovery controlled a non-member daemon")
				}
				switch action {
				case "onestop":
					if !runtime.stopLeavesRunning && (runtime.stopErr == nil || index == 0) {
						delete(runtime.running, daemon)
					}
				case "onestart":
					if !runtime.startLeavesStopped && (runtime.startErr == nil || index == 0) {
						runtime.running[daemon] = true
					}
				default:
					t.Fatal("unexpected service action")
				}
			}
			if action == "onestop" {
				return "", runtime.stopErr
			}
			return "", runtime.startErr
		}
		return run(ctx, path, args, input, env)
	}
	return runtime
}

func TestManagedRecoveryExportPreservesInitialServiceState(t *testing.T) {
	for _, daemons := range [][]string{nil, {"winbindd"}, {"smbd", "winbindd"}, {"nmbd", "smbd", "winbindd"}} {
		t.Run(strings.Join(daemons, "+"), func(t *testing.T) {
			s, paths := newRecoveryTestService(t)
			before := recoverySnapshots(t, paths)
			runtime := fakeRecoveryServiceRuntime(t, s, daemons...)
			ad, _ := s.GetAD()
			file, err := s.ExportRecovery(context.Background(), RecoveryInput{Passphrase: testRecoveryPassphrase, ConfirmComputerName: ad.ComputerName, ManageServices: true})
			if err != nil || len(file) == 0 {
				t.Fatalf("managed export: %v", err)
			}
			var expected []string
			if len(daemons) > 0 {
				expected = []string{"onestop " + strings.Join(daemons, " "), "onestart " + strings.Join(daemons, " ")}
			}
			if !reflect.DeepEqual(runtime.actions, expected) || len(runtime.running) != len(daemons) {
				t.Fatal("managed export did not preserve the original daemon set")
			}
			for _, daemon := range daemons {
				if !runtime.running[daemon] {
					t.Fatal("managed export left a previously running daemon stopped")
				}
			}
			assertRecoveryUnchanged(t, paths, before)
		})
	}
}

func TestManagedRecoveryPreflightDoesNotInterruptServices(t *testing.T) {
	s, paths := newRecoveryTestService(t)
	input := exportTestRecovery(t, s)
	input.ManageServices = true
	before := recoverySnapshots(t, paths)
	runtime := fakeRecoveryServiceRuntime(t, s, "smbd", "winbindd")
	wrong := input
	wrong.ConfirmComputerName = "WRONG"
	if _, err := s.ExportRecovery(context.Background(), wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("managed export ignored computer confirmation")
	}
	if err := s.RestoreRecovery(context.Background(), wrong); !errors.Is(err, ErrInvalid) {
		t.Fatal("managed restore ignored computer confirmation")
	}
	wrong = input
	wrong.Passphrase = "incorrect-recovery-passphrase"
	if err := s.RestoreRecovery(context.Background(), wrong); !errors.Is(err, ErrInvalid) {
		t.Fatal("managed restore accepted the wrong passphrase")
	}
	manual := input
	manual.ManageServices = false
	if _, err := s.ExportRecovery(context.Background(), manual); !errors.Is(err, ErrConflict) {
		t.Fatal("export stopped services without explicit consent")
	}
	if err := s.RestoreRecovery(context.Background(), manual); !errors.Is(err, ErrConflict) {
		t.Fatal("restore stopped services without explicit consent")
	}
	if len(runtime.actions) != 0 || !runtime.running["smbd"] || !runtime.running["winbindd"] {
		t.Fatal("rejected recovery interrupted live services")
	}
	assertRecoveryUnchanged(t, paths, before)
}

func TestManagedRecoveryRestoreAndRollbackResumeOnlyCapturedDaemons(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "restore", true: "rollback"}[rollback], func(t *testing.T) {
			s, _ := newRecoveryTestService(t)
			input := exportTestRecovery(t, s)
			input.ManageServices = true
			if rollback {
				journal, _ := s.recoveryJournal()
				if err := os.WriteFile(journal, input.File, 0600); err != nil {
					t.Fatal(err)
				}
			}
			runtime := fakeRecoveryServiceRuntime(t, s, "smbd", "winbindd")
			held := false
			s.WithServiceSettingsLock = func(operation func() error) error {
				held = true
				defer func() { held = false }()
				return operation()
			}
			runtime.onAction = func(string) {
				if !held {
					t.Fatal("service control raced with service settings")
				}
				if unlock, err := s.LockConfiguration(); !errors.Is(err, ErrBusy) {
					if unlock != nil {
						unlock()
					}
					t.Fatal("service control raced with directory configuration")
				}
			}
			var err error
			if rollback {
				err = s.RollbackRecovery(context.Background(), input)
			} else {
				err = s.RestoreRecovery(context.Background(), input)
			}
			if err != nil || s.RecoveryPending() || !reflect.DeepEqual(runtime.actions, []string{"onestop smbd winbindd", "onestart smbd winbindd"}) || runtime.running["nmbd"] {
				t.Fatalf("managed recovery failed to preserve daemon state: %v", err)
			}
		})
	}
}

func TestManagedRecoveryApplyFailureResumesOnlyAfterCleanRollback(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean rollback", true: "pending rollback"}[persistent], func(t *testing.T) {
			s, paths := newRecoveryTestService(t)
			input := exportTestRecovery(t, s)
			input.ManageServices = true
			before := recoverySnapshots(t, paths)
			runtime := fakeRecoveryServiceRuntime(t, s, "smbd", "winbindd")
			failed := false
			if err := s.DB.Callback().Create().Before("gorm:create").Register("test:managed-recovery-failure", func(tx *gorm.DB) {
				if tx.Statement.Table == "kerberos_keytabs" && (persistent || !failed) {
					failed = true
					tx.AddError(errors.New("isolated managed recovery apply failure"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			if err := s.RestoreRecovery(context.Background(), input); !errors.Is(err, ErrUnavailable) || s.RecoveryPending() != persistent {
				t.Fatalf("managed apply failure: %v", err)
			}
			if !persistent {
				assertRecoveryUnchanged(t, paths, before)
				if !reflect.DeepEqual(runtime.actions, []string{"onestop smbd winbindd", "onestart smbd winbindd"}) {
					t.Fatal("clean rollback did not resume original services")
				}
				return
			}
			if len(runtime.running) != 0 || !reflect.DeepEqual(runtime.actions, []string{"onestop smbd winbindd"}) {
				t.Fatal("incomplete rollback restarted Samba")
			}
			wrong := input
			wrong.Passphrase = "wrong-rollback-passphrase"
			if err := s.RollbackRecovery(context.Background(), wrong); !errors.Is(err, ErrInvalid) {
				t.Fatal("wrong rollback passphrase was accepted")
			}
			if err := s.DB.Callback().Create().Remove("test:managed-recovery-failure"); err != nil {
				t.Fatal(err)
			}
			if err := s.RollbackRecovery(context.Background(), input); err != nil || s.RecoveryPending() || !runtime.running["smbd"] || !runtime.running["winbindd"] || runtime.running["nmbd"] || !reflect.DeepEqual(runtime.actions, []string{"onestop smbd winbindd", "onestart smbd winbindd"}) {
				t.Fatal("rollback did not resume just the services paused by the interrupted operation")
			}
			assertRecoveryUnchanged(t, paths, before)
		})
	}
}

func TestManagedRecoveryServiceFailuresFailClosed(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*recoveryServiceTestRuntime)
		wantApply bool
		wantError error
	}{
		{"unknown state", func(r *recoveryServiceTestRuntime) { r.stateErr = os.ErrPermission }, false, ErrUnavailable},
		{"partial stop", func(r *recoveryServiceTestRuntime) { r.stopErr = errors.New("isolated stop failure") }, false, ErrUnavailable},
		{"stop falsely reports success", func(r *recoveryServiceTestRuntime) { r.stopLeavesRunning = true }, false, ErrConflict},
		{"partial resume", func(r *recoveryServiceTestRuntime) { r.startErr = errors.New("isolated start failure") }, true, ErrUnavailable},
		{"resume falsely reports success", func(r *recoveryServiceTestRuntime) { r.startLeavesStopped = true }, true, ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _ := newRecoveryTestService(t)
			runtime := fakeRecoveryServiceRuntime(t, s, "smbd", "winbindd")
			test.configure(runtime)
			applied := false
			err := s.withRecoveryServices(context.Background(), true, nil, func([]string) error { applied = true; return nil })
			if !errors.Is(err, test.wantError) || applied != test.wantApply {
				t.Fatalf("unsafe service failure handling: %v", err)
			}
			if test.name == "partial stop" && (!runtime.running["smbd"] || !runtime.running["winbindd"] || !reflect.DeepEqual(runtime.actions, []string{"onestop smbd winbindd", "onestart smbd"})) {
				t.Fatal("partial stop did not restore just the daemon it stopped")
			}
		})
	}
}

func TestManagedRecoveryExportRetainsBundleWhenResumingFails(t *testing.T) {
	s, _ := newRecoveryTestService(t)
	runtime := fakeRecoveryServiceRuntime(t, s, "smbd", "winbindd")
	runtime.startErr = errors.New("isolated resume failure")
	ad, _ := s.GetAD()
	file, err := s.ExportRecovery(context.Background(), RecoveryInput{Passphrase: testRecoveryPassphrase, ConfirmComputerName: ad.ComputerName, ManageServices: true})
	if len(file) == 0 || !errors.Is(err, ErrUnavailable) {
		t.Fatal("a resume failure discarded the completed encrypted backup")
	}
	if _, err := openRecovery(file, testRecoveryPassphrase); err != nil {
		t.Fatal("completed backup is not usable")
	}
}

func TestManagedRecoveryResumesAfterRequestCancellation(t *testing.T) {
	s, _ := newRecoveryTestService(t)
	runtime := fakeRecoveryServiceRuntime(t, s, "smbd", "winbindd")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := s.withRecoveryServices(ctx, true, nil, func([]string) error { cancel(); return ctx.Err() })
	if !errors.Is(err, context.Canceled) || !runtime.running["smbd"] || !runtime.running["winbindd"] {
		t.Fatal("browser cancellation stranded services after an unchanged recovery")
	}
}

func TestManagedRecoveryRejectedRestoreResumesWithoutChangingFiles(t *testing.T) {
	s, paths := newRecoveryTestService(t)
	input := exportTestRecovery(t, s)
	input.ManageServices = true
	if err := os.WriteFile(paths["winbindd_idmap.tdb"], []byte("newer isolated allocations"), 0600); err != nil {
		t.Fatal(err)
	}
	before := recoverySnapshots(t, paths)
	runtime := fakeRecoveryServiceRuntime(t, s, "smbd", "winbindd")
	if err := s.RestoreRecovery(context.Background(), input); !errors.Is(err, ErrConflict) || s.RecoveryPending() {
		t.Fatalf("incompatible restore was not safely rejected: %v", err)
	}
	if !runtime.running["smbd"] || !runtime.running["winbindd"] || runtime.running["nmbd"] {
		t.Fatal("rejected restore did not preserve running services")
	}
	assertRecoveryUnchanged(t, paths, before)
}

func TestManagedRecoveryRollbackAfterRestartUsesEncryptedServiceState(t *testing.T) {
	s, _ := newRecoveryTestService(t)
	input := exportTestRecovery(t, s)
	input.ManageServices = true
	run := s.run
	runtime := fakeRecoveryServiceRuntime(t, s, "winbindd")
	if err := s.DB.Callback().Create().Before("gorm:create").Register("test:managed-interruption", func(tx *gorm.DB) {
		if tx.Statement.Table == "kerberos_keytabs" {
			tx.AddError(errors.New("isolated interrupted recovery"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreRecovery(context.Background(), input); !errors.Is(err, ErrUnavailable) || !s.RecoveryPending() || len(runtime.running) != 0 {
		t.Fatal("interrupted recovery did not remain safely stopped")
	}
	journal, _ := s.recoveryJournal()
	previous, err := openRecovery(readTestFile(t, journal), input.Passphrase)
	if err != nil || !slices.Equal(previous.ResumeServices, []string{"winbindd"}) {
		t.Fatal("encrypted journal did not preserve the original running daemon set")
	}
	if err := s.DB.Callback().Create().Remove("test:managed-interruption"); err != nil {
		t.Fatal(err)
	}
	fresh := NewService(s.DB)
	fresh.dataPath, fresh.krbPath, fresh.keytabPath = s.dataPath, s.krbPath, s.keytabPath
	fresh.machineKeytabPath, fresh.nssPath, fresh.smbPath = s.machineKeytabPath, s.nssPath, s.smbPath
	fresh.run = run
	resumed := fakeRecoveryServiceRuntime(t, fresh)
	if err := fresh.RollbackRecovery(context.Background(), input); err != nil || fresh.RecoveryPending() || !reflect.DeepEqual(resumed.actions, []string{"onestart winbindd"}) || len(resumed.running) != 1 {
		t.Fatalf("rollback lost service state across a fresh service instance: %v", err)
	}
	if resumed.running["smbd"] || resumed.running["nmbd"] {
		t.Fatal("rollback started previously stopped daemons")
	}
}

func TestManagedRecoveryImportedBackupCannotSelectServicesToStart(t *testing.T) {
	s, _ := newRecoveryTestService(t)
	input := exportTestRecovery(t, s)
	bundle, err := openRecovery(input.File, input.Passphrase)
	if err != nil {
		t.Fatal(err)
	}
	bundle.ResumeServices = []string{"nmbd", "smbd"}
	input.File, err = sealRecovery(bundle, input.Passphrase)
	if err != nil {
		t.Fatal(err)
	}
	input.ManageServices = true
	runtime := fakeRecoveryServiceRuntime(t, s, "winbindd")
	if err := s.RestoreRecovery(context.Background(), input); err != nil || !reflect.DeepEqual(runtime.actions, []string{"onestop winbindd", "onestart winbindd"}) || runtime.running["nmbd"] || runtime.running["smbd"] {
		t.Fatalf("imported backup changed service resumption policy: %v", err)
	}
}

func TestRecoveryRejectsUnsupportedJournalServiceNamesBeforeInterruptingServices(t *testing.T) {
	s, _ := newRecoveryTestService(t)
	input := exportTestRecovery(t, s)
	bundle, err := openRecovery(input.File, input.Passphrase)
	if err != nil {
		t.Fatal(err)
	}
	bundle.ResumeServices = []string{"sshd"}
	input.File, err = sealRecovery(bundle, input.Passphrase)
	if err != nil {
		t.Fatal(err)
	}
	input.ManageServices = true
	runtime := fakeRecoveryServiceRuntime(t, s, "smbd", "winbindd")
	if err := s.RestoreRecovery(context.Background(), input); !errors.Is(err, ErrInvalid) || len(runtime.actions) != 0 {
		t.Fatal("unrecognized recovery service metadata caused a service interruption")
	}
}

func TestManagedRecoveryCancellationAfterPartialStopResumesSafely(t *testing.T) {
	s, _ := newRecoveryTestService(t)
	runtime := fakeRecoveryServiceRuntime(t, s, "smbd", "winbindd")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime.onAction = func(action string) {
		if action == "onestop" {
			cancel()
		}
	}
	err := s.withRecoveryServices(ctx, true, nil, func([]string) error { t.Fatal("canceled recovery applied state"); return nil })
	if err == nil || !runtime.running["smbd"] || !runtime.running["winbindd"] {
		t.Fatal("cancellation during stop stranded previously running services")
	}
}

func TestManagedRecoveryNeverControlsDomainControllerOrStartsPendingRecovery(t *testing.T) {
	s, _ := newRecoveryTestService(t)
	runtime := fakeRecoveryServiceRuntime(t, s, "samba")
	if err := s.withRecoveryServices(context.Background(), true, nil, func([]string) error { t.Fatal("managed member recovery ran on a DC"); return nil }); !errors.Is(err, ErrConflict) || len(runtime.actions) != 0 {
		t.Fatal("managed recovery stopped a domain controller")
	}
	journal, _ := s.recoveryJournal()
	if err := os.WriteFile(journal, []byte("isolated pending marker"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.recoveryServiceAction(context.Background(), "onestart", []string{"smbd"}); !errors.Is(err, ErrConflict) || len(runtime.actions) != 0 {
		t.Fatal("service action ignored the pending recovery guard")
	}
}

func TestRecoveryServiceScriptPreservesRCFlagsAndLimitsDaemonSet(t *testing.T) {
	for _, test := range []struct {
		name, role  string
		wrongConfig bool
	}{
		{"member", "member server", false},
		{"standalone", "standalone server", false},
		{"domain controller", "active directory domain controller", false},
		{"different rc configuration", "member server", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newTestService(t)
			log := filepath.Join(t.TempDir(), "actions")
			s.recoveryServicePath = filepath.Join(t.TempDir(), "isolated-samba-rc")
			configPath := s.smbPath
			if test.wrongConfig {
				configPath = filepath.Join(t.TempDir(), "other-config")
			}
			fixture := "samba_server_role='" + test.role + "'\nsamba_server_config='" + configPath + "'\nsamba_daemons='nmbd smbd winbindd'\nwinbindd_flags='isolated-custom-flags'\n" +
				"run_rc_command() {\n [ \"$1\" = configtest ] && return 0\n printf '%s|%s|%s\\n' \"$1\" \"$samba_daemons\" \"$winbindd_flags\" >> '" + log + "'\n}\nrun_rc_command \"$1\"\n"
			if err := os.WriteFile(s.recoveryServicePath, []byte(fixture), 0600); err != nil {
				t.Fatal(err)
			}
			s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
				if path != "/bin/sh" || args[5] != s.recoveryServicePath {
					t.Fatal("shell fixture attempted to access real services")
				}
				return runCommand(ctx, path, args, input, env)
			}
			err := s.recoveryServiceAction(context.Background(), "onestart", []string{"winbindd"})
			if test.role == "active directory domain controller" || test.wrongConfig {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatal("rc wrapper allowed an unsupported role or configuration")
				}
				if _, err := os.Stat(log); !os.IsNotExist(err) {
					t.Fatal("rc wrapper acted on an unsupported role or configuration")
				}
				return
			}
			if err != nil || string(readTestFile(t, log)) != "onestart|winbindd|isolated-custom-flags\n" {
				t.Fatal("rc wrapper discarded custom flags or started previously stopped daemons")
			}
		})
	}
}

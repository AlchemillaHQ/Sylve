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
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	"gorm.io/gorm"
)

func saveTestAD(t *testing.T, s *Service, joined bool) directoryModels.ActiveDirectory {
	t.Helper()
	ad, err := s.SaveAD(context.Background(), testADInput())
	if err != nil {
		t.Fatal(err)
	}
	if joined {
		ad.Joined, ad.Enabled = true, true
		if err := s.DB.Save(&ad).Error; err != nil {
			t.Fatal(err)
		}
	}
	return ad
}

func healthyCommands(t *testing.T, s *Service, password string) func(context.Context, string, []string, string, []string) (string, error) {
	t.Helper()
	return func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if password != "" && strings.Contains(strings.Join(args, " "), password) {
			t.Fatal("password leaked to argv")
		}
		if password != "" && strings.Contains(strings.Join(env, " "), password) {
			t.Fatal("password leaked to environment")
		}
		joinedArgs := strings.Join(args, " ")
		switch path {
		case "/usr/bin/kinit":
			if input != "" && input != password+"\n" {
				t.Fatalf("unexpected kinit input %q", input)
			}
			if len(args) < 3 || args[0] != "-c" || !strings.HasPrefix(args[1], "FILE:") {
				t.Fatalf("ticket not isolated: %v", args)
			}
			if err := os.WriteFile(strings.TrimPrefix(args[1], "FILE:"), []byte("test ticket"), 0600); err != nil {
				t.Fatal(err)
			}
			return "", nil
		case "/usr/local/bin/net":
			if joinedArgs == "--version" {
				return "Version 4.23.11", nil
			}
			if strings.Contains(joinedArgs, "ads info") {
				return "LDAP server: dc.example.com\nServer time offset: 0\n", nil
			}
			if strings.Contains(joinedArgs, "ads lookup") {
				return "Domain: example.com\nPre-Win2k Domain: EXAMPLE\nClient Site Name: Default-First-Site-Name\n", nil
			}
			if strings.Contains(joinedArgs, "ads keytab create") {
				if !strings.Contains(joinedArgs, "sync_kvno:machine_password") {
					t.Fatalf("keytab rotation lacks required flags: %v", args)
				}
				return "", os.WriteFile(legacyKeytabDestination(args), nativeKeytab(t, 1), 0600)
			}
			if strings.Contains(joinedArgs, "ads join") {
				if !strings.Contains(joinedArgs, "--use-kerberos=required") || !strings.Contains(joinedArgs, "--use-krb5-ccache=FILE:") {
					t.Fatalf("join did not require isolated Kerberos ticket: %v", args)
				}
				return "Joined", nil
			}
			if strings.Contains(joinedArgs, "ads testjoin") || strings.Contains(joinedArgs, "ads leave") {
				return "OK", nil
			}
		case "/usr/local/bin/testparm":
			return "[global]\n", nil
		case "/usr/local/bin/wbinfo":
			return "OK", nil
		}
		t.Fatalf("unexpected command %s %v", path, args)
		return "", errors.New("unexpected command")
	}
}

func TestJoinHealthDisableAndLeaveLifecycle(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	saveTestAD(t, s, false)
	password := "do-not-persist-me"
	s.run = healthyCommands(t, s, password)
	if err := s.Join(ctx, JoinInput{Username: "joinuser", Password: password}); err != nil {
		t.Fatal(err)
	}
	ad, err := s.GetAD()
	if err != nil || !ad.Joined || !ad.Enabled || ad.RealmID == 0 || ad.MembershipPending != "" {
		t.Fatalf("join state: %+v %v", ad, err)
	}
	if !strings.Contains(string(readTestFile(t, s.nssPath)), "passwd: files winbind") {
		t.Fatal("did not integrate NSS")
	}
	keytabs, err := s.ListKeytabs()
	if err != nil || len(keytabs) != 1 || !keytabs[0].Managed {
		t.Fatalf("missing managed keytab: %+v %v", keytabs, err)
	}
	if err := s.DeleteKeytab(ctx, keytabs[0].ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted machine credentials: %v", err)
	}
	dir, _ := s.runtimeDir()
	files, _ := os.ReadDir(dir)
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "ticket-") {
			t.Fatal("left join ticket behind")
		}
	}
	for _, path := range []string{s.smbPath, s.krbPath, s.nssPath} {
		if strings.Contains(string(readTestFile(t, path)), password) {
			t.Fatalf("password persisted in %s", path)
		}
	}
	checked, err := s.Status(ctx, true)
	if err != nil || checked.State != "HEALTHY" {
		t.Fatalf("health: %+v %v", checked, err)
	}
	cached, err := s.Status(ctx, false)
	if err != nil || cached.CheckedAt.Before(checked.CheckedAt.Add(-1000000000)) || cached.CheckedAt.After(checked.CheckedAt) {
		t.Fatalf("cached check invented new timestamp: %+v %+v", checked, cached)
	}
	if err := s.SetEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	ad, _ = s.GetAD()
	if !ad.Joined || ad.Enabled {
		t.Fatal("disable lost domain membership")
	}
	if strings.Contains(string(readTestFile(t, s.nssPath)), "winbind") {
		t.Fatal("disable retained managed NSS integration")
	}
	if err := s.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Leave(ctx, LeaveInput{}); err != nil {
		t.Fatal(err)
	}
	ad, _ = s.GetAD()
	if ad.Joined || ad.Enabled || ad.MembershipPending != "" {
		t.Fatalf("leave state: %+v", ad)
	}
	keytabs, _ = s.ListKeytabs()
	if len(keytabs) != 0 {
		t.Fatal("leave retained managed keytab")
	}
	if _, err := os.Stat(s.machineKeytabPath); !os.IsNotExist(err) {
		t.Fatal("machine keytab was not removed")
	}
}

func TestFailedAuthenticationRestoresConfiguration(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	saveTestAD(t, s, false)
	before := map[string]string{}
	for _, path := range []string{s.smbPath, s.krbPath, s.nssPath} {
		before[path] = string(readTestFile(t, path))
	}
	password := "secret-password"
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if len(args) == 1 && args[0] == "--version" {
			return "Version 4.23.11", nil
		}
		if path == "/usr/bin/kinit" {
			return password, errors.New("bad credentials: " + password)
		}
		if strings.Contains(strings.Join(args, " "), "ads lookup") {
			return "Domain: example.com\nPre-Win2k Domain: EXAMPLE\n", nil
		}
		return "Server time offset: 0", nil
	}
	err := s.Join(ctx, JoinInput{Username: "joinuser", Password: password})
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), password) {
		t.Fatalf("unsafe authentication error: %v", err)
	}
	ad, _ := s.GetAD()
	if ad.Enabled || ad.Joined || ad.RealmID != 0 || ad.MembershipPending != "" {
		t.Fatalf("failed join persisted state: %+v", ad)
	}
	for path, text := range before {
		if string(readTestFile(t, path)) != text {
			t.Fatalf("failed join did not restore %s", path)
		}
	}
	realms, _ := s.ListRealms()
	if len(realms) != 0 {
		t.Fatal("failed join retained an automatically created realm")
	}
	status, _ := s.Status(ctx, false)
	if status.State != "FAULTED" || strings.Contains(status.Error, password) {
		t.Fatalf("unsafe fault status: %+v", status)
	}
}

func TestPartialJoinIsRetainedAndRecoverable(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	saveTestAD(t, s, false)
	healthy := healthyCommands(t, s, "test-join-password")
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		text := strings.Join(args, " ")
		if strings.Contains(text, "ads join") || strings.Contains(text, "ads testjoin") {
			return "DC unavailable", errors.New("network timeout")
		}
		return healthy(ctx, path, args, input, env)
	}
	if err := s.Join(ctx, JoinInput{Username: "joinuser", Password: "test-join-password"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("partial join: %v", err)
	}
	ad, _ := s.GetAD()
	if ad.MembershipPending != "join" || !ad.Enabled || ad.Joined {
		t.Fatalf("lost partial membership: %+v", ad)
	}
	if _, err := s.SaveAD(ctx, testADInput()); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed incomplete membership: %v", err)
	}
	s.run = healthy
	status, err := s.Status(ctx, true)
	if err != nil || status.State != "HEALTHY" || !status.Joined {
		t.Fatalf("failed pending join recovery: %+v %v", status, err)
	}
	ad, _ = s.GetAD()
	if ad.MembershipPending != "" || !ad.Joined {
		t.Fatal("did not persist recovered membership")
	}
}

func TestJoinDNSAndClockPreflight(t *testing.T) {
	for _, mode := range []string{"DNS", "clock skew", "missing clock"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestService(t)
			saveTestAD(t, s, false)
			if mode == "DNS" {
				s.lookupSRV = func(context.Context, string, string, string) (string, []*net.SRV, error) {
					return "", nil, errors.New("no SRV records")
				}
			}
			s.run = func(_ context.Context, _ string, args []string, _ string, _ []string) (string, error) {
				if len(args) == 1 && args[0] == "--version" {
					return "Version 4.23.11", nil
				}
				if mode == "clock skew" {
					return "Server time offset: -600 seconds", nil
				}
				return "no clock information", nil
			}
			if err := s.Join(context.Background(), JoinInput{Username: "joinuser", Password: "password"}); err == nil {
				t.Fatal("joined without valid DNS/time checks")
			}
			ad, _ := s.GetAD()
			if ad.Joined || ad.Enabled {
				t.Fatal("preflight failure changed membership")
			}
		})
	}
}

func TestIDMappingsCannotReinterpretSavedPermissions(t *testing.T) {
	for _, joined := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved permissions", true: "joined"}[joined], func(t *testing.T) {
			s := newTestService(t)
			saveTestAD(t, s, joined)
			s.SharePrincipalsInUse = func() (bool, error) { return !joined, nil }
			input := testADInput()
			input.RangeLow++
			if _, err := s.SaveAD(context.Background(), input); !errors.Is(err, ErrConflict) {
				t.Fatalf("reinterpreted ACL ownership: %v", err)
			}
		})
	}
}

func TestADInputRejectsInvalidAndOverlappingMappings(t *testing.T) {
	s := newTestService(t)
	for _, modify := range []func(*ADInput){
		func(i *ADInput) { i.Domain = "example.com\nsecurity = user" },
		func(i *ADInput) { i.ComputerName = "this-name-is-too-long" },
		func(i *ADInput) { i.RangeLow = 1000 },
		func(i *ADInput) { i.IDMapBackend = "ldap" },
		func(i *ADInput) { i.IDMapBackend = "ad"; i.IDMapSchema = "injected\n" },
		func(i *ADInput) {
			i.AllowTrustedDomains = true
			i.TrustedDomains = []directoryModels.IDMap{{Domain: "TRUSTED", Backend: "rid", RangeLow: 1000001, RangeHigh: 2000000}}
		},
		func(i *ADInput) { i.IDMapBackend = "autorid"; i.RangeHigh = 1000010 },
	} {
		input := testADInput()
		modify(&input)
		if _, err := s.SaveAD(context.Background(), input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %+v: %v", input, err)
		}
	}
}

func TestDisabledSambaBlocksJoinAndEnable(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, false)
	if err := s.DB.Exec("UPDATE basic_settings SET services = '[]'").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.Join(context.Background(), JoinInput{Username: "joinuser", Password: "secret"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("joined with Samba disabled: %v", err)
	}
	ad, _ := s.GetAD()
	ad.Joined = true
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(context.Background(), true); !errors.Is(err, ErrConflict) {
		t.Fatalf("enabled with Samba disabled: %v", err)
	}
}

func TestEnableFailureRestoresPreviousSettingsAndNSS(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	if err := s.writeNSS(true); err != nil {
		t.Fatal(err)
	}
	s.WriteSamba = func(context.Context, bool) error {
		ad, _ := s.GetAD()
		if !ad.Enabled {
			return errors.New("restart failed")
		}
		return nil
	}
	if err := s.SetEnabled(context.Background(), false); err == nil {
		t.Fatal("expected apply failure")
	}
	ad, _ := s.GetAD()
	if !ad.Joined || !ad.Enabled {
		t.Fatal("lost membership during rollback")
	}
	if !strings.Contains(string(readTestFile(t, s.nssPath)), "winbind") {
		t.Fatal("did not restore NSS after failure")
	}
}

func TestOutageLeaveRequiresExplicitLocalRemoval(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	if err := s.writeNSS(true); err != nil {
		t.Fatal(err)
	}
	s.run = func(context.Context, string, []string, string, []string) (string, error) {
		return "offline", errors.New("unavailable")
	}
	if err := s.Leave(context.Background(), LeaveInput{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("leave during outage: %v", err)
	}
	ad, _ := s.GetAD()
	if !ad.Joined || ad.MembershipPending != "leave" {
		t.Fatal("lost unresolved leave state")
	}
	s.run = func(context.Context, string, []string, string, []string) (string, error) {
		t.Fatal("local-only removal contacted domain")
		return "", nil
	}
	if err := s.Leave(context.Background(), LeaveInput{LocalOnly: true}); err != nil {
		t.Fatal(err)
	}
	ad, _ = s.GetAD()
	if ad.Joined || ad.Enabled || ad.MembershipPending != "" {
		t.Fatalf("local cleanup failed: %+v", ad)
	}
}

func TestMachineCredentialRotationAndOfflineRebootRecovery(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	ctx := context.Background()
	first := nativeKeytab(t, 1)
	if err := os.WriteFile(s.machineKeytabPath, first, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.syncMachineKeytab(ctx); err != nil {
		t.Fatal(err)
	}
	rotated := nativeKeytab(t, 2)
	if err := os.WriteFile(s.machineKeytabPath, rotated, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.syncMachineKeytab(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.machineKeytabPath); err != nil {
		t.Fatal(err)
	}
	if err := s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if string(readTestFile(t, s.machineKeytabPath)) != string(rotated) {
		t.Fatal("reboot restored outdated machine credentials")
	}
	if _, err := os.Stat(filepath.Join(s.dataPath, "directory-services", "keytab.key")); err != nil {
		t.Fatal(err)
	}
}

func TestLeaveCleanupFailureCanBeRetriedWithoutRemoteCredentials(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	s.run = healthyCommands(t, s, "")
	write := s.WriteSamba
	s.WriteSamba = func(context.Context, bool) error { return errors.New("restart failed") }
	if err := s.Leave(context.Background(), LeaveInput{}); err == nil {
		t.Fatal("expected cleanup failure")
	}
	ad, _ := s.GetAD()
	if ad.Joined || ad.Enabled || ad.MembershipPending != "leave-cleanup" {
		t.Fatalf("lost pending cleanup: %+v", ad)
	}
	s.run = func(context.Context, string, []string, string, []string) (string, error) {
		t.Fatal("cleanup retry contacted the domain")
		return "", nil
	}
	s.WriteSamba = write
	if err := s.Leave(context.Background(), LeaveInput{}); err != nil {
		t.Fatal(err)
	}
	ad, _ = s.GetAD()
	if ad.MembershipPending != "" {
		t.Fatal("cleanup retry retained pending operation")
	}
}

func TestFailedLeaveAfterUnconfirmedJoinKeepsRemoteCleanupPending(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, false)
	ad.Enabled, ad.MembershipPending = true, "join"
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.run = func(_ context.Context, path string, args []string, _ string, _ []string) (string, error) {
		if path == "/usr/local/bin/testparm" {
			return "[global]\n", nil
		}
		if path == "/usr/local/bin/net" && len(args) == 1 && args[0] == "--version" {
			return "Version 4.23.11", nil
		}
		if path != "/usr/local/bin/net" || !strings.Contains(strings.Join(args, " "), "ads leave") {
			t.Fatalf("unexpected command: %s %v", path, args)
		}
		calls++
		return "offline", errors.New("DC unavailable")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := s.Leave(context.Background(), LeaveInput{}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("leave retry silently became local-only removal: %v", err)
		}
		ad, err := s.GetAD()
		if err != nil || ad.MembershipPending != "leave" || !ad.Enabled {
			t.Fatalf("lost unresolved remote membership: %+v %v", ad, err)
		}
	}
	if calls != 2 {
		t.Fatalf("remote cleanup was not retried: %d", calls)
	}
	if err := s.Leave(context.Background(), LeaveInput{LocalOnly: true}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("explicit local-only removal contacted the domain")
	}
}

func TestLeavePersistsRemoteCompletionBeforeDatabaseCleanup(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	if err := os.WriteFile(s.machineKeytabPath, nativeKeytab(t, 1), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.syncMachineKeytab(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.run = healthyCommands(t, s, "")
	const callback = "test:fail_machine_keytab_cleanup"
	if err := s.DB.Callback().Delete().Before("gorm:delete").Register(callback, func(tx *gorm.DB) {
		tx.AddError(errors.New("keytab database cleanup failed"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Leave(context.Background(), LeaveInput{}); err == nil {
		t.Fatal("expected database cleanup failure")
	}
	ad, err := s.GetAD()
	if err != nil || ad.Joined || ad.Enabled || ad.MembershipPending != "leave-cleanup" {
		t.Fatalf("lost completed remote leave: %+v %v", ad, err)
	}
	if err := s.Join(context.Background(), JoinInput{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("joined before finishing leave cleanup: %v", err)
	}
	if err := s.DB.Callback().Delete().Remove(callback); err != nil {
		t.Fatal(err)
	}
	s.run = func(context.Context, string, []string, string, []string) (string, error) {
		t.Fatal("database cleanup retry contacted the domain")
		return "", nil
	}
	if err := s.Leave(context.Background(), LeaveInput{}); err != nil {
		t.Fatal(err)
	}
	keytabs, err := s.ListKeytabs()
	if err != nil || len(keytabs) != 0 {
		t.Fatalf("managed credentials were not removed: %+v %v", keytabs, err)
	}
}

func TestPreflightRejectsUnsupportedSambaVersions(t *testing.T) {
	for _, version := range []string{"Version 4.15.13", "Version 3.6.25", "unknown version"} {
		s := newTestService(t)
		ad := saveTestAD(t, s, false)
		s.run = func(context.Context, string, []string, string, []string) (string, error) { return version, nil }
		if err := s.Preflight(context.Background(), ad); err == nil {
			t.Fatalf("accepted unsupported Samba: %s", version)
		}
	}
}

func TestConfirmedJoinRetainsMarkerUntilLocalSetupSucceeds(t *testing.T) {
	for _, recoverBy := range []string{"join retry", "health check"} {
		t.Run(recoverBy, func(t *testing.T) {
			s := newTestService(t)
			saveTestAD(t, s, false)
			s.run = healthyCommands(t, s, "test-join-password")
			write := s.WriteSamba
			s.WriteSamba = func(ctx context.Context, restart bool) error {
				if restart {
					return errors.New("local Samba restart failed")
				}
				return write(ctx, restart)
			}
			if err := s.Join(context.Background(), JoinInput{Username: "joinuser", Password: "test-join-password"}); err == nil {
				t.Fatal("expected local setup failure")
			}
			ad, _ := s.GetAD()
			if !ad.Joined || !ad.Enabled || ad.MembershipPending != "join" {
				t.Fatalf("lost confirmed but incomplete join: %+v", ad)
			}
			s.WriteSamba = write
			healthy := s.run
			s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
				if strings.Contains(strings.Join(args, " "), "ads join") || path == "/usr/bin/kinit" {
					t.Fatal("local recovery attempted remote enrollment")
				}
				return healthy(ctx, path, args, input, env)
			}
			if recoverBy == "join retry" {
				if err := s.Join(context.Background(), JoinInput{}); err != nil {
					t.Fatal(err)
				}
			} else {
				status, err := s.Status(context.Background(), true)
				if err != nil || !status.Joined || status.State != "HEALTHY" {
					t.Fatalf("recovery status: %+v %v", status, err)
				}
			}
			ad, _ = s.GetAD()
			if !ad.Joined || ad.MembershipPending != "" {
				t.Fatalf("incomplete recovery: %+v", ad)
			}
		})
	}
}

func TestJoinRecoveryRequiresSambaToRemainEnabled(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, true)
	ad.MembershipPending = "join"
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Exec("UPDATE basic_settings SET services = '[]'").Error; err != nil {
		t.Fatal(err)
	}
	s.run = healthyCommands(t, s, "")
	status, err := s.Status(context.Background(), true)
	if err != nil || status.State != "FAULTED" || !strings.Contains(status.Error, "enable the Samba service") {
		t.Fatalf("recovered membership with Samba disabled: %+v %v", status, err)
	}
	ad, err = s.GetAD()
	if err != nil || ad.MembershipPending != "join" {
		t.Fatalf("lost the recovery marker: %+v %v", ad, err)
	}
}

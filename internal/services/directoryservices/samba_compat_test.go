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
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
)

func legacyKeytab(t *testing.T, kvno uint32) []byte {
	t.Helper()
	files := [][]byte{}
	for _, principal := range []string{"SYLVE$@EXAMPLE.COM", "host/sylve@EXAMPLE.COM", "host/sylve.example.com@EXAMPLE.COM", "cifs/sylve@EXAMPLE.COM", "cifs/sylve.example.com@EXAMPLE.COM"} {
		files = append(files, testKeytab(principal, kvno, byte(kvno)))
	}
	data, err := mergeKeytabs(files...)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func legacyKeytabDestination(args []string) string {
	path := ""
	for _, arg := range args {
		if value, ok := strings.CutPrefix(arg, "--option=dedicated keytab file="); ok {
			path = value
		}
	}
	return path
}

func legacyCommands(t *testing.T, s *Service, version, password string, kvno *uint32) commandRunner {
	t.Helper()
	healthy := healthyCommands(t, s, password)
	s.WriteSamba = func(context.Context, bool) error {
		ad, err := s.GetAD()
		if err != nil {
			return err
		}
		text, err := SambaConfig(ad, version)
		if err != nil {
			return err
		}
		return os.WriteFile(s.smbPath, []byte(text), 0644)
	}
	return func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if path != "/usr/local/bin/net" {
			return healthy(ctx, path, args, input, env)
		}
		if slices.Equal(args, []string{"--version"}) {
			return version, nil
		}
		text := strings.Join(args, " ")
		if strings.Contains(text, "ads lookup") {
			return healthy(ctx, path, args, input, env)
		}
		if strings.Contains(text, "sync machine password to keytab") || !slices.Contains(args, "--option=kerberos method=secrets only") {
			t.Fatalf("legacy command uses native keytab maintenance: %v", args)
		}
		if !strings.Contains(text, "ads keytab") {
			return healthy(ctx, path, args, input, env)
		}
		target := legacyKeytabDestination(args)
		if target == "" || target == s.machineKeytabPath || target == s.keytabPath || filepath.Dir(filepath.Dir(target)) != filepath.Dir(s.machineKeytabPath) {
			t.Fatalf("legacy command did not use isolated staging: %v", args)
		}
		for _, arg := range []string{"--machine-pass", "--use-kerberos=required", "--option=kerberos method=dedicated keytab"} {
			if !slices.Contains(args, arg) {
				t.Fatalf("missing %s: %v", arg, args)
			}
		}
		for _, file := range []string{target, filepath.Dir(target)} {
			info, err := os.Stat(file)
			if err != nil || info.Mode().Perm()&0077 != 0 {
				t.Fatalf("staging credentials are not private: %s %v", file, err)
			}
		}
		previous := readTestFile(t, target)
		var fresh []byte
		switch {
		case strings.HasSuffix(text, "ads keytab create"):
			fresh = testKeytab("SYLVE$@EXAMPLE.COM", *kvno, byte(*kvno))
		case strings.HasSuffix(text, "ads keytab add host cifs"):
			fresh = legacyKeytab(t, *kvno)
		default:
			t.Fatalf("unexpected legacy keytab command: %v", args)
		}
		data, err := mergeKeytabs(previous, fresh)
		if err != nil {
			t.Fatal(err)
		}
		return "", os.WriteFile(target, data, 0600)
	}
}

func TestSambaVersionSelectsKeytabWorkflow(t *testing.T) {
	for _, test := range []struct {
		version string
		native  bool
		err     error
	}{
		{"Version 4.16.11-FreeBSD", false, nil},
		{"Version 4.19.9", false, nil},
		{"Version 4.20.9", false, nil},
		{"Version 4.21.0", true, nil},
		{"Version 4.23.11", true, nil},
		{"Version 5.0.0", true, nil},
		{"Version 4.15.13", false, ErrInvalid},
		{"Version 3.6.25", false, ErrInvalid},
		{"unknown version", false, ErrUnavailable},
		{"Version 4.210oops", false, ErrUnavailable},
		{"Version 999999999999999999999999999999999999.16.0", false, ErrUnavailable},
	} {
		t.Run(test.version, func(t *testing.T) {
			native, err := supportsMachineKeytabSync(test.version)
			if native != test.native || !errors.Is(err, test.err) {
				t.Fatalf("version detection: %v %v", native, err)
			}
			s := newTestService(t)
			ad := saveTestAD(t, s, false)
			s.run = func(context.Context, string, []string, string, []string) (string, error) { return test.version, nil }
			if err := s.Preflight(context.Background(), ad); !errors.Is(err, test.err) {
				t.Fatalf("preflight: %v", err)
			}
		})
	}
}

func TestSambaConfigAndCommandsFollowVersionChanges(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, true)
	for _, version := range []string{"Version 4.16.11", "Version 4.21.0", "Version 4.20.9"} {
		native, _ := supportsMachineKeytabSync(version)
		text, err := SambaConfig(ad, version)
		if err != nil || strings.Contains(text, "sync machine password to keytab") != native {
			t.Fatalf("wrong config for %s: %v\n%s", version, err, text)
		}
		if !strings.Contains(text, "idmap config EXAMPLE : backend = rid") || !strings.Contains(text, "client ldap sasl wrapping = seal") {
			t.Fatal("compatibility changed mapping or LDAP protection")
		}
		if !native && (!strings.Contains(text, "kerberos method = secrets only") || strings.Contains(text, "dedicated keytab file")) {
			t.Fatal("legacy Samba can overwrite the merged system keytab")
		}
		if strings.Contains(text, "machine password timeout") || strings.Contains(text, "allow_weak_crypto") {
			t.Fatal("compatibility disables rotation or weakens authentication")
		}
		s.run = func(_ context.Context, path string, args []string, _ string, _ []string) (string, error) {
			if path == "/usr/local/bin/testparm" && native {
				return "[global]\n", nil
			}
			if path != "/usr/local/bin/net" {
				t.Fatalf("unexpected command: %s", path)
			}
			if slices.Equal(args, []string{"--version"}) {
				return version, nil
			}
			if strings.Contains(strings.Join(args, " "), "sync machine password to keytab") != native {
				t.Fatalf("wrong command options for %s: %v", version, args)
			}
			if !slices.Contains(args, "--option=security=ads") || !slices.Contains(args, "--option=netbios name=SYLVE") {
				t.Fatal("disabled membership tools lost explicit domain configuration")
			}
			return "OK", nil
		}
		// The options must also work against a standalone smb.conf while disabled.
		ad.Enabled = false
		if _, err := s.netCommand(context.Background(), ad, nil, "ads", "testjoin"); err != nil {
			t.Fatal(err)
		}
		ad.Enabled = true
	}
	if text, err := SambaConfig(directoryModels.ActiveDirectory{}, "unknown"); err != nil || text != "" {
		t.Fatal("standalone configuration now requires AD tooling")
	}
}

func TestSambaTrustedDomainDiscoveryFollowsTrustSupport(t *testing.T) {
	for _, version := range []string{"Version 4.16.11", "Version 4.20.9", "Version 4.21.0", "Version 4.23.11"} {
		for _, backend := range []string{"rid", "ad", "autorid"} {
			for _, trusted := range []bool{false, true} {
				for _, disableCache := range []bool{false, true} {
					ad := normalizeAD(testADInput())
					ad.Enabled, ad.IDMapBackend = true, backend
					ad.AllowTrustedDomains, ad.DisableCache = trusted, disableCache
					text, err := SambaConfig(ad, version)
					if err != nil {
						t.Fatal(err)
					}
					for _, setting := range []string{"allow trusted domains", "winbind scan trusted domains"} {
						if !strings.Contains(text, setting+" = "+yesNo(trusted)+"\n") {
							t.Fatalf("%s/%s/trusted=%t/disableCache=%t: missing %s", version, backend, trusted, disableCache, setting)
						}
					}
					if !strings.Contains(text, "winbind enum users = no\nwinbind enum groups = no\n") || !strings.Contains(text, "client ldap sasl wrapping = seal\n") {
						t.Fatal("trust discovery enabled NSS enumeration or weakened LDAP protection")
					}
				}
			}
		}
	}
}

func TestLegacyJoinHealthDisableLeaveAndRejoin(t *testing.T) {
	for _, version := range []string{"Version 4.16.11", "Version 4.20.9"} {
		for _, enrollment := range []string{"password", "keytab"} {
			t.Run(version+"/"+enrollment, func(t *testing.T) {
				s := newTestService(t)
				ctx := context.Background()
				input := testADInput()
				join := JoinInput{Username: "joinuser", Password: "legacy-join-password"}
				if enrollment == "keytab" {
					keytab, err := s.SaveKeytab(ctx, 0, KeytabInput{Name: "enrollment", File: base64.StdEncoding.EncodeToString(testKeytab("joinuser@EXAMPLE.COM", 1, 9))})
					if err != nil {
						t.Fatal(err)
					}
					input.KeytabID, input.KerberosPrincipal = keytab.ID, "joinuser@EXAMPLE.COM"
					join = JoinInput{}
				}
				if _, err := s.SaveAD(ctx, input); err != nil {
					t.Fatal(err)
				}
				kvno := uint32(1)
				s.run = legacyCommands(t, s, version, join.Password, &kvno)
				if err := s.Join(ctx, join); err != nil {
					t.Fatal(err)
				}
				ad, _ := s.GetAD()
				if !ad.Joined || !ad.Enabled || ad.MembershipPending != "" {
					t.Fatalf("join state: %+v", ad)
				}
				if err := validateLegacyMachineKeytab(readTestFile(t, s.machineKeytabPath), ad); err != nil {
					t.Fatal(err)
				}
				if status, err := s.Status(ctx, true); err != nil || status.State != "HEALTHY" {
					t.Fatalf("legacy health: %+v %v", status, err)
				}
				for _, enabled := range []bool{false, true} {
					if err := s.SetEnabled(ctx, enabled); err != nil {
						t.Fatal(err)
					}
				}
				if err := s.Leave(ctx, LeaveInput{}); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(s.machineKeytabPath); !os.IsNotExist(err) {
					t.Fatal("leave retained machine keytab")
				}
				kvno++
				if err := s.Join(ctx, join); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestLegacyRotationAndOfflineRecoveryPreserveMergedKeytabs(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, true)
	ctx := context.Background()
	unmanaged := testKeytab("unmanaged/service@OTHER.COM", 1, 8)
	if err := os.WriteFile(s.keytabPath, unmanaged, 0600); err != nil {
		t.Fatal(err)
	}
	kvno := uint32(1)
	s.run = legacyCommands(t, s, "Version 4.16.11", "", &kvno)
	if err := s.syncMachineKeytab(ctx); err != nil {
		t.Fatal(err)
	}
	kvno++
	if err := s.syncMachineKeytab(ctx); err != nil {
		t.Fatal(err)
	}
	rotated := readTestFile(t, s.machineKeytabPath)
	entries, err := parseKeytab(rotated)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []uint32{1, 2} {
		if !slices.ContainsFunc(entries, func(e keytabEntry) bool {
			return e.principal == "cifs/sylve.example.com@EXAMPLE.COM" && e.kvno == version
		}) {
			t.Fatalf("rotation lost CIFS KVNO %d", version)
		}
	}
	if err := s.writeKerberos(s.DB); err != nil {
		t.Fatal(err)
	}
	merged := readTestFile(t, s.keytabPath)
	entries, err = parseKeytab(merged)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(keytabPrincipals(entries), "unmanaged/service@OTHER.COM") {
		t.Fatal("legacy rotation overwrote unmanaged system keys")
	}
	for _, recoverBy := range []string{"reconcile", "monitor"} {
		t.Run(recoverBy, func(t *testing.T) {
			for _, path := range []string{s.machineKeytabPath, s.keytabPath} {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			monitorCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			s.run = func(_ context.Context, path string, args []string, _ string, _ []string) (string, error) {
				if path == "/usr/local/bin/net" && slices.Equal(args, []string{"--version"}) {
					return "Version 4.16.11", nil
				}
				if !strings.Contains(strings.Join(args, " "), "ads keytab create") {
					t.Fatalf("offline recovery attempted enrollment or rotation: %s %v", path, args)
				}
				cancel()
				return "DC offline", errors.New("network timeout")
			}
			if recoverBy == "reconcile" {
				if err := s.Reconcile(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				s.Monitor(monitorCtx)
			}
			if !bytes.Equal(rotated, readTestFile(t, s.machineKeytabPath)) || !bytes.Equal(merged, readTestFile(t, s.keytabPath)) {
				t.Fatal("outage recovery lost the latest saved credentials")
			}
			status, _ := s.Status(ctx, false)
			if status.State != "FAULTED" || !status.Joined {
				t.Fatalf("outage was not reported: %+v", status)
			}
		})
	}
	ad.Enabled = false
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	s.run = func(context.Context, string, []string, string, []string) (string, error) {
		t.Fatal("disabled membership attempted a keytab refresh")
		return "", nil
	}
	if err := s.syncMachineKeytab(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyRefreshFailureNeverPublishesPartialKeys(t *testing.T) {
	stale, err := mergeKeytabs(legacyKeytab(t, 1), testKeytab("SYLVE$@EXAMPLE.COM", 2, 2))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, command string
		data          []byte
	}{
		{"create failure", "ads keytab create", nil},
		{"add failure", "ads keytab add host cifs", nil},
		{"empty", "", []byte{5, 2}},
		{"malformed", "", []byte("invalid keytab")},
		{"stale services", "", stale},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newTestService(t)
			saveTestAD(t, s, true)
			kvno := uint32(1)
			healthy := legacyCommands(t, s, "Version 4.16.11", "", &kvno)
			s.run = healthy
			ctx := context.Background()
			if err := s.syncMachineKeytab(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.writeKerberos(s.DB); err != nil {
				t.Fatal(err)
			}
			before := readTestFile(t, s.machineKeytabPath)
			merged := readTestFile(t, s.keytabPath)
			var saved directoryModels.KerberosKeytab
			if err := s.DB.Where("managed = ?", true).First(&saved).Error; err != nil {
				t.Fatal(err)
			}
			kvno++
			s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
				text := strings.Join(args, " ")
				if test.command != "" && strings.HasSuffix(text, test.command) {
					return "DC unavailable", errors.New("failed after a partial update")
				}
				out, err := healthy(ctx, path, args, input, env)
				if err != nil || test.data == nil || !strings.HasSuffix(text, "ads keytab add host cifs") {
					return out, err
				}
				return out, os.WriteFile(legacyKeytabDestination(args), test.data, 0600)
			}
			if err := s.syncMachineKeytab(ctx); err == nil {
				t.Fatal("accepted failed or incomplete legacy refresh")
			}
			var after directoryModels.KerberosKeytab
			if err := s.DB.First(&after, saved.ID).Error; err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, readTestFile(t, s.machineKeytabPath)) || !bytes.Equal(merged, readTestFile(t, s.keytabPath)) || !bytes.Equal(saved.Ciphertext, after.Ciphertext) {
				t.Fatal("failed refresh overwrote usable credentials")
			}
			staging, _ := filepath.Glob(filepath.Join(filepath.Dir(s.machineKeytabPath), "sylve-keytab-*"))
			if len(staging) != 0 {
				t.Fatalf("staging credentials were not cleaned up: %v", staging)
			}
		})
	}
}

func TestLegacyJoinKeytabFailureRetriesOnlyLocalSetup(t *testing.T) {
	for _, recoverBy := range []string{"join retry", "health check"} {
		t.Run(recoverBy, func(t *testing.T) {
			s := newTestService(t)
			saveTestAD(t, s, false)
			kvno := uint32(1)
			healthy := legacyCommands(t, s, "Version 4.16.11", "legacy-join-password", &kvno)
			s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
				if strings.HasSuffix(strings.Join(args, " "), "ads keytab add host cifs") {
					return "offline", errors.New("keytab update failed")
				}
				return healthy(ctx, path, args, input, env)
			}
			ctx := context.Background()
			if err := s.Join(ctx, JoinInput{Username: "joinuser", Password: "legacy-join-password"}); err == nil {
				t.Fatal("expected keytab setup failure")
			}
			ad, _ := s.GetAD()
			if !ad.Joined || !ad.Enabled || ad.MembershipPending != "join" {
				t.Fatalf("lost confirmed membership: %+v", ad)
			}
			s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
				if path == "/usr/bin/kinit" || strings.Contains(strings.Join(args, " "), "ads join") {
					t.Fatal("local keytab recovery repeated remote enrollment")
				}
				return healthy(ctx, path, args, input, env)
			}
			if recoverBy == "join retry" {
				if err := s.Join(ctx, JoinInput{}); err != nil {
					t.Fatal(err)
				}
			} else if status, err := s.Status(ctx, true); err != nil || status.State != "HEALTHY" {
				t.Fatalf("keytab recovery failed: %+v %v", status, err)
			}
			ad, _ = s.GetAD()
			if !ad.Joined || ad.MembershipPending != "" {
				t.Fatalf("recovery state: %+v", ad)
			}
		})
	}
}

func TestLegacyMachineKeytabRequiresMatchingMemberAndServiceKeys(t *testing.T) {
	ad := normalizeAD(testADInput())
	for _, test := range []struct {
		principal string
		kvno      uint32
	}{
		{"cifs/other@EXAMPLE.COM", 2},
		{"cifs/sylve@OTHER.COM", 2},
		{"cifs/sylve@EXAMPLE.COM", 1},
	} {
		data, err := mergeKeytabs(testKeytab("SYLVE$@EXAMPLE.COM", 2, 2), testKeytab("host/sylve@EXAMPLE.COM", 2, 2), testKeytab(test.principal, test.kvno, 2))
		if err != nil {
			t.Fatal(err)
		}
		if err := validateLegacyMachineKeytab(data, ad); err == nil {
			t.Fatalf("accepted mismatched service keys: %s KVNO %d", test.principal, test.kvno)
		}
	}
}

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
	"strconv"
	"strings"
	"testing"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
)

func nativeKeytab(t *testing.T, kvno uint32, hostnames ...string) []byte {
	t.Helper()
	files := [][]byte{}
	for _, principal := range machineKeytabPrincipals(normalizeAD(testADInput()), hostnames...) {
		files = append(files, testKeytab(principal, kvno, byte(kvno)))
	}
	data, err := mergeKeytabs(files...)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func nativeServiceKeytab(t *testing.T, kvno uint32, hostnames ...string) []byte {
	t.Helper()
	files := [][]byte{}
	for _, principal := range machineKeytabPrincipals(normalizeAD(testADInput()), hostnames...) {
		if !strings.HasPrefix(principal, "SYLVE$") {
			files = append(files, testKeytab(principal, kvno, byte(kvno)))
		}
	}
	data, err := mergeKeytabs(files...)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestNativeMachineKeytabSyncUsesOneCompleteRule(t *testing.T) {
	ad := normalizeAD(testADInput())
	ad.Enabled = true
	hostnames, err := SambaKeytabHostnames("[global]\n dns hostname = files.example.com\n netbios aliases = FILES ARCHIVE\n additional dns hostnames = other.example.com files.example.com\n", ad.Domain)
	if err != nil {
		t.Fatal(err)
	}
	path := "/private dir/machine.keytab"
	rule, err := strconv.Unquote(machineKeytabSync(path, ad, hostnames...))
	if err != nil || !strings.HasPrefix(rule, path+":spns=") || !strings.HasSuffix(rule, ":sync_kvno:machine_password") {
		t.Fatalf("invalid native rule: %q %v", rule, err)
	}
	if strings.Count(rule, ":spns=") != 1 || strings.Contains(rule, "spn_prefixes") || strings.Contains(rule, "account_name:") || strings.Contains(rule, "netbios_aliases") {
		t.Fatal("native rule contains conflicting or ineffective SPN specifiers")
	}
	for _, principal := range []string{"SYLVE$@EXAMPLE.COM", "host/SYLVE@EXAMPLE.COM", "cifs/sylve.example.com@EXAMPLE.COM", "host/FILES@EXAMPLE.COM", "cifs/archive.example.com@EXAMPLE.COM", "cifs/files.example.com@EXAMPLE.COM", "host/other.example.com@EXAMPLE.COM"} {
		if strings.Count(rule, principal) != 1 {
			t.Fatalf("missing or duplicated principal %s: %s", principal, rule)
		}
	}
	config, err := SambaConfig(ad, "Version 4.23.11", hostnames...)
	if err != nil || !strings.Contains(config, "SYLVE$@EXAMPLE.COM") || !strings.Contains(config, "cifs/FILES@EXAMPLE.COM") {
		t.Fatalf("generated configuration lost principals: %v\n%s", err, config)
	}
}

func TestNativeMachineKeytabRejectsUnsafeAliasNames(t *testing.T) {
	for _, text := range []string{"dns hostname = host:account_name", "additional dns hostnames = host@example.com", "netbios aliases = alias,other", "netbios aliases = ../../file"} {
		if _, err := SambaKeytabHostnames(text, "EXAMPLE.COM"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted unsafe alias %q: %v", text, err)
		}
	}
}

func TestNativeMachineKeytabRequiresCurrentKeysForEveryPrincipal(t *testing.T) {
	ad := normalizeAD(testADInput())
	for _, principal := range machineKeytabPrincipals(ad, "alias.example.com") {
		t.Run(principal, func(t *testing.T) {
			files := [][]byte{}
			for _, name := range machineKeytabPrincipals(ad, "alias.example.com") {
				kvno := uint32(2)
				if name == principal {
					kvno = 1
				}
				files = append(files, testKeytab(name, kvno, byte(kvno)))
			}
			data, err := mergeKeytabs(files...)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateNativeMachineKeytab(data, ad, "alias.example.com"); err == nil {
				t.Fatalf("accepted stale %s", principal)
			}
		})
	}
	if err := validateNativeMachineKeytab(nativeKeytab(t, 2, "alias.example.com"), ad, "alias.example.com"); err != nil {
		t.Fatal(err)
	}
}

func saveObsoleteNativeKeytab(t *testing.T, s *Service) directoryModels.KerberosKeytab {
	t.Helper()
	ad := saveTestAD(t, s, true)
	ad.Enabled = false
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.machineKeytabPath, nativeServiceKeytab(t, 1, "alias.example.com"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.syncMachineKeytab(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.writeKerberos(s.DB); err != nil {
		t.Fatal(err)
	}
	ad.Enabled = true
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	var row directoryModels.KerberosKeytab
	if err := s.DB.Where("managed = ?", true).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func nativeStagingCommands(t *testing.T, s *Service, data []byte, failure error) commandRunner {
	t.Helper()
	return func(_ context.Context, path string, args []string, input string, _ []string) (string, error) {
		if input != "" {
			t.Fatal("keytab repair requested enrollment credentials")
		}
		if path == "/usr/local/bin/testparm" {
			if !slices.Contains(args, "--option=realm=EXAMPLE.COM") || !slices.Contains(args, "--option=netbios name=SYLVE") {
				t.Fatalf("aliases use another member identity: %v", args)
			}
			return "[global]\nadditional dns hostnames = alias.example.com\n", nil
		}
		if path == "/usr/local/bin/net" && slices.Equal(args, []string{"--version"}) {
			return "Version 4.23.11", nil
		}
		if path != "/usr/local/bin/net" || !strings.HasSuffix(strings.Join(args, " "), "ads keytab create") || !slices.Contains(args, "--machine-pass") {
			t.Fatalf("repair attempted another operation: %s %v", path, args)
		}
		target := legacyKeytabDestination(args)
		if target == s.machineKeytabPath || target == s.keytabPath || filepath.Dir(filepath.Dir(target)) != filepath.Dir(s.machineKeytabPath) {
			t.Fatalf("repair did not isolate its output: %v", args)
		}
		for _, file := range []string{target, filepath.Dir(target)} {
			info, err := os.Stat(file)
			if err != nil || info.Mode().Perm()&0077 != 0 {
				t.Fatalf("staging credentials are not private: %s %v", file, err)
			}
		}
		rule := ""
		for _, arg := range args {
			if value, ok := strings.CutPrefix(arg, "--option=sync machine password to keytab="); ok {
				rule = value
			}
		}
		ad := normalizeAD(testADInput())
		if rule != machineKeytabSync(target, ad, "alias.example.com") {
			t.Fatalf("staging sync rule lost machine or service aliases: %s", rule)
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			t.Fatal(err)
		}
		return "", failure
	}
}

func TestNativeMachineKeytabRepairsExistingServiceOnlyFile(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := os.WriteFile(s.keytabPath, testKeytab("unmanaged/service@OTHER.COM", 1, 8), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveKeytab(ctx, 0, KeytabInput{Name: "enrollment", File: base64.StdEncoding.EncodeToString(testKeytab("joinuser@EXAMPLE.COM", 1, 9))}); err != nil {
		t.Fatal(err)
	}
	saved := saveObsoleteNativeKeytab(t, s)
	fresh := nativeKeytab(t, 2, "alias.example.com")
	s.run = nativeStagingCommands(t, s, fresh, nil)
	if err := s.syncMachineKeytab(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.writeKerberos(s.DB); err != nil {
		t.Fatal(err)
	}
	var row directoryModels.KerberosKeytab
	if err := s.DB.First(&row, saved.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !row.Managed || !slices.Contains(row.Principals, "SYLVE$@EXAMPLE.COM") || bytes.Equal(row.Ciphertext, saved.Ciphertext) {
		t.Fatal("repair did not update the encrypted managed credentials")
	}
	entries, err := parseKeytab(readTestFile(t, s.keytabPath))
	if err != nil || !slices.Contains(keytabPrincipals(entries), "joinuser@EXAMPLE.COM") || !slices.Contains(keytabPrincipals(entries), "unmanaged/service@OTHER.COM") {
		t.Fatalf("repair lost non-machine system keys: %v", err)
	}
	merged := readTestFile(t, s.keytabPath)
	for _, path := range []string{s.machineKeytabPath, s.keytabPath} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	s.run = func(_ context.Context, path string, args []string, _ string, _ []string) (string, error) {
		if path == "/usr/local/bin/net" && slices.Equal(args, []string{"--version"}) {
			return "Version 4.23.11", nil
		}
		t.Fatal("saved complete native keys should recover without contacting the DC")
		return "", errors.New("DC offline")
	}
	if err := s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fresh, readTestFile(t, s.machineKeytabPath)) || !bytes.Equal(merged, readTestFile(t, s.keytabPath)) {
		t.Fatal("recovery did not retain the repaired saved keytabs")
	}
}

func TestNativeMachineKeytabFailedRepairPreservesInstalledAndSavedKeys(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
		err  error
	}{
		{"command failure", []byte("partial"), errors.New("DC unavailable")},
		{"empty", []byte{5, 2}, nil},
		{"malformed", []byte("not a keytab"), nil},
		{"missing machine", nativeServiceKeytab(t, 2, "alias.example.com"), nil},
		{"missing aliases", nativeKeytab(t, 2), nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newTestService(t)
			saved := saveObsoleteNativeKeytab(t, s)
			before := readTestFile(t, s.machineKeytabPath)
			merged := readTestFile(t, s.keytabPath)
			s.run = nativeStagingCommands(t, s, test.data, test.err)
			if err := s.syncMachineKeytab(context.Background()); err == nil {
				t.Fatal("accepted failed or incomplete native repair")
			}
			var after directoryModels.KerberosKeytab
			if err := s.DB.First(&after, saved.ID).Error; err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, readTestFile(t, s.machineKeytabPath)) || !bytes.Equal(merged, readTestFile(t, s.keytabPath)) || !bytes.Equal(saved.Ciphertext, after.Ciphertext) {
				t.Fatal("failed repair overwrote last-good credentials")
			}
			staging, _ := filepath.Glob(filepath.Join(filepath.Dir(s.machineKeytabPath), "sylve-keytab-*"))
			if len(staging) != 0 {
				t.Fatalf("staging credentials were not cleaned up: %v", staging)
			}
		})
	}
}

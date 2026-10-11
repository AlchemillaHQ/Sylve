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
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	"github.com/alchemillahq/sylve/internal/testutil"
)

// Every writable path and command is isolated. These tests must never enroll
// the developer's host or alter its Kerberos/NSS/Samba configuration.
func newTestService(t *testing.T) *Service {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t, &directoryModels.ActiveDirectory{}, &directoryModels.KerberosRealm{}, &directoryModels.KerberosKeytab{}, &directoryModels.KerberosSettings{}, &models.BasicSettings{})
	s := NewService(db)
	dir := t.TempDir()
	s.dataPath = dir
	s.krbPath, s.keytabPath, s.machineKeytabPath = filepath.Join(dir, "krb5.conf"), filepath.Join(dir, "krb5.keytab"), filepath.Join(dir, "machine.keytab")
	s.nssPath, s.smbPath = filepath.Join(dir, "nsswitch.conf"), filepath.Join(dir, "smb4.conf")
	for path, text := range map[string]string{s.krbPath: "# original kerberos\n", s.nssPath: "passwd: files\ngroup: files\nhosts: files dns\n", s.smbPath: "# original samba\n"} {
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s.run = func(_ context.Context, path string, args []string, _ string, _ []string) (string, error) {
		if path == "/usr/local/bin/net" && slices.Equal(args, []string{"--version"}) {
			return "Version 4.23.11", nil
		}
		t.Fatal("unexpected external command")
		return "", errors.New("unexpected command")
	}
	s.lookupSRV = func(context.Context, string, string, string) (string, []*net.SRV, error) {
		return "", []*net.SRV{{Target: "dc.example.com.", Port: 88}}, nil
	}
	s.ValidateSamba = func(context.Context, directoryModels.ActiveDirectory) error { return nil }
	s.WriteSamba = func(context.Context, bool) error {
		ad, err := s.GetAD()
		if err != nil {
			return err
		}
		text, err := SambaConfig(ad, "Version 4.23.11")
		if err != nil {
			return err
		}
		return os.WriteFile(s.smbPath, []byte(text), 0644)
	}
	if err := db.Create(&models.BasicSettings{Services: []models.AvailableService{models.SambaServer}}).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func testADInput() ADInput {
	return ADInput{Domain: "example.com", Workgroup: "EXAMPLE", ComputerName: "SYLVE", IDMapBackend: "rid", RangeLow: 1000000, RangeHigh: 1999999999, IDMapSchema: "rfc2307"}
}

func testKeytab(principal string, kvno uint32, key byte) []byte {
	name, realm, _ := strings.Cut(principal, "@")
	parts := strings.Split(name, "/")
	var entry bytes.Buffer
	write := func(value any) { _ = binary.Write(&entry, binary.BigEndian, value) }
	text := func(value string) { write(uint16(len(value))); entry.WriteString(value) }
	write(uint16(len(parts)))
	text(realm)
	for _, part := range parts {
		text(part)
	}
	write(uint32(1))
	write(uint32(0))
	write(uint8(kvno))
	write(uint16(18))
	write(uint16(32))
	entry.Write(bytes.Repeat([]byte{key}, 32))
	write(kvno)
	var out bytes.Buffer
	out.Write([]byte{5, 2})
	_ = binary.Write(&out, binary.BigEndian, int32(entry.Len()))
	out.Write(entry.Bytes())
	return out.Bytes()
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReconcileFreshInstallIsNoOp(t *testing.T) {
	s := newTestService(t)
	before := readTestFile(t, s.krbPath)
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, readTestFile(t, s.krbPath)) {
		t.Fatal("rewrote unconfigured Kerberos")
	}
	if _, err := os.Stat(s.keytabPath); !os.IsNotExist(err) {
		t.Fatal("created unconfigured system keytab")
	}
	if _, err := os.Stat(s.nssPath + ".pre-sylve"); !os.IsNotExist(err) {
		t.Fatal("changed unconfigured NSS")
	}
}

func TestRealmsAndSettingsPersistence(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	realm, err := s.SaveRealm(ctx, 0, RealmInput{Realm: "example.com", KDC: []string{"dc.example.com", "dc.example.com", "[2001:db8::1]:88"}})
	if err != nil {
		t.Fatal(err)
	}
	if realm.Realm != "EXAMPLE.COM" || len(realm.KDC) != 2 {
		t.Fatalf("invalid normalized realm: %+v", realm)
	}
	if _, err := s.SaveRealm(ctx, 0, RealmInput{Realm: "EXAMPLE.COM"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate realm: %v", err)
	}
	input := testADInput()
	input.RealmID = realm.ID
	if _, err := s.SaveAD(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRealm(ctx, realm.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted referenced realm: %v", err)
	}
	if _, err := s.SaveRealm(ctx, realm.ID, RealmInput{Realm: "OTHER.COM"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("renamed referenced realm: %v", err)
	}
	settings := directoryModels.KerberosSettings{AppDefaults: "pam = {\n forwardable = true\n}", LibDefaults: "ticket_lifetime = 8h\nrenew_lifetime = 2d\nrdns = true"}
	if err := s.SaveKerberosSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	text := string(readTestFile(t, s.krbPath))
	if strings.Count(text, "ticket_lifetime =") != 1 || !strings.Contains(text, "ticket_lifetime = 8h") || !strings.Contains(text, "default_realm = EXAMPLE.COM") {
		t.Fatalf("advanced values did not override defaults:\n%s", text)
	}
	saved, err := s.GetKerberosSettings()
	if err != nil || saved.LibDefaults != settings.LibDefaults {
		t.Fatalf("settings: %+v %v", saved, err)
	}
	if string(readTestFile(t, s.krbPath+".pre-sylve")) != "# original kerberos\n" {
		t.Fatal("lost original Kerberos configuration")
	}
}

func TestInvalidKerberosDefaults(t *testing.T) {
	for _, test := range []struct {
		text string
		app  bool
	}{
		{"[libdefaults]\nrdns = false", false}, {"include /etc/other.conf", false}, {"rdns = false\x00", false}, {"rdns = maybe", false}, {"ticket_lifetime = forever", false}, {"clockskew = -1", false}, {"default_keytab_name = /tmp/other", false}, {"rdns = false\\", false}, {"x = {\nforwardable = true", true}, {"x = {\ny = {\nz = {\n}\n}\n}", true}, {"unknown = true", true}, {"default_tkt_enctypes = made-up-enctype", false},
	} {
		t.Run(test.text, func(t *testing.T) {
			if err := validateKerberosDefaults(test.text, test.app); !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected validation error, got %v", err)
			}
		})
	}
}

func TestKeytabEncryptionReplacementAndProtection(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	baseline := testKeytab("legacy@OTHER.COM", 1, 1)
	if err := os.WriteFile(s.keytabPath, baseline, 0600); err != nil {
		t.Fatal(err)
	}
	data := testKeytab("joinuser@EXAMPLE.COM", 258, 2)
	row, err := s.SaveKeytab(ctx, 0, KeytabInput{Name: "join", File: base64.StdEncoding.EncodeToString(data)})
	if err != nil {
		t.Fatal(err)
	}
	var stored directoryModels.KerberosKeytab
	if err := s.DB.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(stored.Ciphertext, data) || bytes.Contains(stored.Ciphertext, bytes.Repeat([]byte{2}, 32)) {
		t.Fatal("keytab was stored in plaintext")
	}
	decrypted, err := s.decryptKeytab(stored.Ciphertext)
	if err != nil || !bytes.Equal(decrypted, data) {
		t.Fatalf("encrypted round trip: %v", err)
	}
	entries, err := parseKeytab(readTestFile(t, s.keytabPath))
	if err != nil || len(entries) != 2 {
		t.Fatalf("baseline not merged: %v", err)
	}
	listed, err := s.ListKeytabs()
	if err != nil || len(listed) != 1 || listed[0].Ciphertext != nil {
		t.Fatalf("keytab listing: %+v %v", listed, err)
	}
	encoded, _ := json.Marshal(listed)
	if bytes.Contains(encoded, []byte("Ciphertext")) || bytes.Contains(encoded, []byte("ciphertext")) || bytes.Contains(encoded, []byte(base64.StdEncoding.EncodeToString(data))) {
		t.Fatal("API exposed key material")
	}
	for _, path := range []string{s.keytabPath, s.keytabPath + ".pre-sylve", filepath.Join(s.dataPath, "directory-services", "keytab.key")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("unsafe mode on %s: %v", path, err)
		}
	}
	input := testADInput()
	input.KeytabID, input.KerberosPrincipal = row.ID, row.Principals[0]
	if _, err := s.SaveAD(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteKeytab(ctx, row.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted referenced keytab: %v", err)
	}
	if _, err := s.SaveKeytab(ctx, row.ID, KeytabInput{Name: "renamed", File: base64.StdEncoding.EncodeToString(testKeytab("other@EXAMPLE.COM", 1, 3))}); !errors.Is(err, ErrConflict) {
		t.Fatalf("removed referenced principal: %v", err)
	}
	if _, err := s.SaveKeytab(ctx, row.ID, KeytabInput{Name: "renamed", File: base64.StdEncoding.EncodeToString(testKeytab("joinuser@EXAMPLE.COM", 259, 3))}); err != nil {
		t.Fatal(err)
	}
	stored.Managed = true
	if err := s.DB.Save(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveKeytab(ctx, stored.ID, KeytabInput{Name: "managed"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("edited managed keytab: %v", err)
	}
	if _, err := s.SaveKeytab(ctx, 0, KeytabInput{Name: "shadow", File: base64.StdEncoding.EncodeToString(data)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("shadowed managed principal: %v", err)
	}
}

func TestKerberosMutationRollsBackFilesAndDatabase(t *testing.T) {
	s := newTestService(t)
	baseline := testKeytab("legacy@OTHER.COM", 1, 1)
	if err := os.WriteFile(s.keytabPath, baseline, 0600); err != nil {
		t.Fatal(err)
	}
	s.krbPath = filepath.Join(s.dataPath, "missing-parent", "krb5.conf")
	if _, err := s.SaveKeytab(context.Background(), 0, KeytabInput{Name: "service", File: base64.StdEncoding.EncodeToString(testKeytab("service@EXAMPLE.COM", 1, 2))}); err == nil {
		t.Fatal("expected file write failure")
	}
	rows, err := s.ListKeytabs()
	if err != nil || len(rows) != 0 {
		t.Fatalf("failed upload persisted: %+v %v", rows, err)
	}
	if !bytes.Equal(baseline, readTestFile(t, s.keytabPath)) {
		t.Fatal("failed upload changed system keytab")
	}
}

func TestMissingEncryptionKeyIsNotReplaced(t *testing.T) {
	s := newTestService(t)
	if _, err := s.SaveKeytab(context.Background(), 0, KeytabInput{Name: "first", File: base64.StdEncoding.EncodeToString(testKeytab("first@EXAMPLE.COM", 1, 1))}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dataPath, "directory-services", "keytab.key")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveKeytab(context.Background(), 0, KeytabInput{Name: "second", File: base64.StdEncoding.EncodeToString(testKeytab("second@EXAMPLE.COM", 1, 2))}); err == nil {
		t.Fatal("silently generated an unrelated encryption key")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("replaced missing encryption key")
	}
}

func TestNSSPreservesLocalSourcesAndUnrelatedContent(t *testing.T) {
	original := "# host configuration\npasswd: files # local users\ngroup: files\nhosts: files dns\nshells: files\n"
	enabled := nssConfig(original, true, original)
	if strings.Count(enabled, "winbind") != 2 || !strings.Contains(enabled, "hosts: files dns") {
		t.Fatalf("invalid NSS configuration:\n%s", enabled)
	}
	if nssConfig(enabled, true, original) != enabled {
		t.Fatal("NSS enable is not idempotent")
	}
	disabled := nssConfig(enabled, false, original)
	if strings.Contains(disabled, "winbind") || !strings.Contains(disabled, "# local users") {
		t.Fatalf("invalid NSS cleanup:\n%s", disabled)
	}
	manual := "passwd: files winbind\ngroup: files winbind\n"
	if !reflect.DeepEqual(nssConfig(manual, false, manual), manual) {
		t.Fatal("removed pre-existing manually configured Winbind sources")
	}
}

func TestNSSFreeBSDCompatSources(t *testing.T) {
	original := "passwd: compat # local users\npasswd_compat: nis\ngroup: compat\ngroup_compat: nis\nhosts: files dns\n"
	enabled := nssConfig(original, true, original)
	if !strings.Contains(enabled, "passwd: files winbind # local users") || !strings.Contains(enabled, "group: files winbind\n") {
		t.Fatalf("compat cannot be combined with Winbind:\n%s", enabled)
	}
	if nssConfig(enabled, true, original) != enabled {
		t.Fatal("compat enable is not idempotent")
	}
	if disabled := nssConfig(enabled, false, original); disabled != original {
		t.Fatalf("did not restore compat sources:\n%s", disabled)
	}
	modified := strings.Replace(enabled, "passwd: files winbind", "passwd: files cache winbind", 1)
	if disabled := nssConfig(modified, false, original); !strings.Contains(disabled, "passwd: files cache # local users") {
		t.Fatalf("discarded an administrator's additional source:\n%s", disabled)
	}
	legacy := strings.ReplaceAll(original, "compat #", "compat winbind #")
	legacy = strings.Replace(legacy, "group: compat\n", "group: compat winbind\n", 1)
	if nssConfig(legacy, true, original) != enabled {
		t.Fatal("did not repair a previously generated compat/Winbind combination")
	}
}

func TestRealmDiscoveryDoesNotSaveConfiguration(t *testing.T) {
	s := newTestService(t)
	s.lookupSRV = func(_ context.Context, service, protocol, name string) (string, []*net.SRV, error) {
		if protocol != "tcp" || name != "example.com" {
			t.Fatalf("wrong discovery: %s %s", protocol, name)
		}
		switch service {
		case "kerberos":
			return "", []*net.SRV{{Target: "dc1.example.com.", Port: 88}, {Target: "dc2.example.com.", Port: 8888}}, nil
		case "kpasswd":
			return "", []*net.SRV{{Target: "dc1.example.com.", Port: 464}}, nil
		default:
			return "", nil, errors.New("no administration SRV records")
		}
	}
	input, err := s.DiscoverRealm(context.Background(), "example.com")
	if err != nil || input.Realm != "EXAMPLE.COM" || !reflect.DeepEqual(input.KDC, []string{"dc1.example.com", "dc2.example.com:8888"}) || len(input.AdminServer) != 0 || len(input.KpasswdServer) != 1 {
		t.Fatalf("DNS discovery: %+v %v", input, err)
	}
	realms, _ := s.ListRealms()
	if len(realms) != 0 {
		t.Fatal("discovery saved configuration")
	}
}

func TestCredentialFailuresDoNotReturnPartialSecrets(t *testing.T) {
	s := newTestService(t)
	password := "private-credential-that-must-not-be-printed"
	s.run = func(context.Context, string, []string, string, []string) (string, error) {
		return "truncated password: " + password[:12], errors.New("partial secret: " + password[:12])
	}
	_, err := s.command(context.Background(), "/usr/bin/kinit", nil, password+"\n", nil)
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), password[:12]) {
		t.Fatalf("credential command exposed output: %v", err)
	}
}

func TestFailedKeytabAuthenticationRemovesTemporaryCredentials(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	keytab, err := s.SaveKeytab(ctx, 0, KeytabInput{Name: "join", File: base64.StdEncoding.EncodeToString(testKeytab("joinuser@EXAMPLE.COM", 1, 1))})
	if err != nil {
		t.Fatal(err)
	}
	input := testADInput()
	input.KeytabID, input.KerberosPrincipal = keytab.ID, keytab.Principals[0]
	ad, err := s.SaveAD(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	s.run = func(_ context.Context, path string, args []string, secret string, env []string) (string, error) {
		if path != "/usr/bin/kinit" || !slices.Contains(args, "-k") || secret != "" {
			t.Fatalf("unexpected keytab authentication: %s %v", path, args)
		}
		return "authentication failed", errors.New("bad keytab")
	}
	if _, _, err := s.ticket(ctx, ad, JoinInput{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected authentication failure: %v", err)
	}
	dir, err := s.runtimeDir()
	if err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "ticket-") {
			t.Fatalf("failed authentication retained its temporary directory: %s", file.Name())
		}
	}
}

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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
)

func savedCredentialInput(password string) ADInput {
	input := testADInput()
	username := "joinuser"
	input.JoinUsername, input.JoinPassword = &username, &password
	return input
}

func TestSavedJoinCredentialsEncryptionAndMetadata(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	password := "saved-test-enrollment-password"
	input := savedCredentialInput(password)
	first, err := s.SaveAD(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.JoinUsername != "joinuser" || !first.JoinPasswordConfigured || bytes.Contains(first.JoinPasswordCiphertext, []byte(password)) {
		t.Fatal("saved credentials are not encrypted or missing metadata")
	}
	second, err := s.SaveAD(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.JoinPasswordCiphertext, second.JoinPasswordCiphertext) {
		t.Fatal("password encryption reused a nonce")
	}
	for _, ad := range []directoryModels.ActiveDirectory{first, second} {
		data, err := s.decryptSecret(ad.JoinPasswordCiphertext, joinPasswordPurpose(ad))
		if err != nil || string(data) != password {
			t.Fatalf("saved password failed to round trip: %v", err)
		}
		clear(data)
		encoded, err := json.Marshal(ad)
		if err != nil || bytes.Contains(encoded, []byte(password)) || bytes.Contains(encoded, []byte("ciphertext")) || bytes.Contains(encoded, []byte("Ciphertext")) || bytes.Contains(encoded, []byte(base64.StdEncoding.EncodeToString(ad.JoinPasswordCiphertext))) {
			t.Fatal("credential metadata exposed secret material")
		}
	}
	read, err := s.GetAD()
	if err != nil || !read.JoinPasswordConfigured || !bytes.Equal(read.JoinPasswordCiphertext, second.JoinPasswordCiphertext) {
		t.Fatalf("saved credential metadata was not recovered: %v", err)
	}
	for path, mode := range map[string]os.FileMode{filepath.Join(s.dataPath, "directory-services"): 0700, filepath.Join(s.dataPath, "directory-services", "keytab.key"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("unsafe credential key storage: %s %v", path, err)
		}
	}
	for _, path := range []string{s.smbPath, s.krbPath, s.nssPath} {
		if bytes.Contains(readTestFile(t, path), []byte(password)) {
			t.Fatalf("password was written into runtime configuration: %s", path)
		}
	}
}

func TestSavedJoinCredentialsRetainReplaceAndClear(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	before, err := s.SaveAD(ctx, savedCredentialInput("first-enrollment-password"))
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []ADInput{testADInput(), savedCredentialInput("")} {
		updated, err := s.SaveAD(ctx, input)
		if err != nil || updated.JoinUsername != before.JoinUsername || !updated.JoinPasswordConfigured || !bytes.Equal(before.JoinPasswordCiphertext, updated.JoinPasswordCiphertext) {
			t.Fatalf("blank or omitted password did not retain credentials: %v", err)
		}
	}
	replaced, err := s.SaveAD(ctx, savedCredentialInput("new-enrollment-password"))
	if err != nil || bytes.Equal(before.JoinPasswordCiphertext, replaced.JoinPasswordCiphertext) {
		t.Fatalf("password replacement failed: %v", err)
	}
	input := testADInput()
	input.ClearJoinPassword = true
	cleared, err := s.SaveAD(ctx, input)
	if err != nil || cleared.JoinUsername != before.JoinUsername || cleared.JoinPasswordConfigured || len(cleared.JoinPasswordCiphertext) != 0 {
		t.Fatalf("explicit password removal failed: %v", err)
	}
	read, err := s.GetAD()
	if err != nil || read.JoinPasswordConfigured || len(read.JoinPasswordCiphertext) != 0 {
		t.Fatal("password clear was not persisted")
	}
}

func TestSavedJoinCredentialsIdentityChangesRequireReplacement(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	before, err := s.SaveAD(ctx, savedCredentialInput("test-identity-password"))
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range []bool{false, true} {
		input := testADInput()
		if domain {
			input.Domain = "other.example.com"
		} else {
			username := "otheruser"
			input.JoinUsername = &username
		}
		if _, err := s.SaveAD(ctx, input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("reused credentials with a different identity: %v", err)
		}
		after, _ := s.GetAD()
		if after.JoinUsername != before.JoinUsername || after.Domain != before.Domain || !bytes.Equal(before.JoinPasswordCiphertext, after.JoinPasswordCiphertext) {
			t.Fatal("invalid identity edit changed saved credentials")
		}
	}
	input := savedCredentialInput("replacement-identity-password")
	username := "otheruser"
	input.JoinUsername = &username
	input.Domain = "other.example.com"
	updated, err := s.SaveAD(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.decryptSecret(updated.JoinPasswordCiphertext, joinPasswordPurpose(updated))
	if err != nil || string(data) != "replacement-identity-password" {
		t.Fatalf("explicit credential replacement failed: %v", err)
	}
}

func TestSavedJoinCredentialsValidationDoesNotEchoPasswords(t *testing.T) {
	for _, test := range []struct {
		name, username, password string
		clear                    bool
	}{
		{"missing username", "", "test-private-password", false},
		{"missing password", "joinuser", "", false},
		{"argument username", "-joinuser", "test-private-password", false},
		{"samba username separator", "DOMAIN\\joinuser", "test-private-password", false},
		{"password separator", "joinuser", "private\npassword", false},
		{"oversized password", "joinuser", strings.Repeat("x", 4097), false},
		{"replace and clear", "joinuser", "test-private-password", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newTestService(t)
			input := testADInput()
			input.JoinUsername, input.JoinPassword = &test.username, &test.password
			input.ClearJoinPassword = test.clear
			_, err := s.SaveAD(context.Background(), input)
			if !errors.Is(err, ErrInvalid) || (test.password != "" && strings.Contains(err.Error(), test.password)) {
				t.Fatal("invalid credentials were accepted or exposed in an error")
			}
			var count int64
			if err := s.DB.Model(&directoryModels.ActiveDirectory{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("invalid credentials were persisted")
			}
		})
	}
}

func TestSavedJoinCredentialsAreUsedWithoutTemporaryOverrides(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	password := "saved-lifecycle-enrollment-password"
	before, err := s.SaveAD(ctx, savedCredentialInput(password))
	if err != nil {
		t.Fatal(err)
	}
	s.run = healthyCommands(t, s, password)
	for range 2 {
		if err := s.Join(ctx, JoinInput{}); err != nil {
			t.Fatal(err)
		}
		if err := s.Leave(ctx, LeaveInput{}); err != nil {
			t.Fatal(err)
		}
		ad, _ := s.GetAD()
		if ad.Joined || !ad.JoinPasswordConfigured || !bytes.Equal(before.JoinPasswordCiphertext, ad.JoinPasswordCiphertext) {
			t.Fatal("leave or rejoin discarded saved enrollment credentials")
		}
	}
}

func TestSavedJoinCredentialsTemporaryOverrideIsNotPersisted(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	ad, err := s.SaveAD(ctx, savedCredentialInput("original-saved-password"))
	if err != nil {
		t.Fatal(err)
	}
	s.run = func(_ context.Context, path string, args []string, input string, env []string) (string, error) {
		if path != "/usr/bin/kinit" || !slices.Contains(args, "override@EXAMPLE.COM") || input != "temporary-override-password\n" || strings.Contains(strings.Join(args, " ")+strings.Join(env, " "), "temporary-override-password") {
			t.Fatal("temporary override did not use isolated stdin authentication")
		}
		return "", nil
	}
	_, cleanup, err := s.ticket(ctx, ad, JoinInput{Username: "override", Password: "temporary-override-password"})
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	after, _ := s.GetAD()
	if after.JoinUsername != ad.JoinUsername || !bytes.Equal(ad.JoinPasswordCiphertext, after.JoinPasswordCiphertext) {
		t.Fatal("temporary credential override replaced saved credentials")
	}
	if _, _, err := s.ticket(ctx, ad, JoinInput{Username: "override"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("partial overrides borrowed a saved password for another account")
	}
}

func TestSavedJoinCredentialsFailurePreservesLastGoodConfiguration(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	password := "saved-failed-join-password"
	before, err := s.SaveAD(ctx, savedCredentialInput(password))
	if err != nil {
		t.Fatal(err)
	}
	baseline := map[string][]byte{}
	for _, path := range []string{s.krbPath, s.smbPath, s.nssPath} {
		baseline[path] = readTestFile(t, path)
	}
	healthy := healthyCommands(t, s, password)
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if path == "/usr/bin/kinit" {
			return password[:12], errors.New(password[:12])
		}
		return healthy(ctx, path, args, input, env)
	}
	if err := s.Join(ctx, JoinInput{}); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), password[:12]) {
		t.Fatal("saved-password authentication failure exposed credentials")
	}
	after, _ := s.GetAD()
	if after.Enabled || after.Joined || after.MembershipPending != "" || !bytes.Equal(before.JoinPasswordCiphertext, after.JoinPasswordCiphertext) {
		t.Fatal("failed saved-credential join changed enrollment configuration")
	}
	for path, text := range baseline {
		if !bytes.Equal(text, readTestFile(t, path)) {
			t.Fatal("failed saved-credential join changed runtime files")
		}
	}
	files, _ := os.ReadDir(filepath.Join(s.dataPath, "directory-services"))
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "ticket-") {
			t.Fatal("failed saved-credential join retained temporary tickets")
		}
	}
}

func TestSavedJoinCredentialsEncryptionIsBoundToIdentityAndPurpose(t *testing.T) {
	s := newTestService(t)
	ad, err := s.SaveAD(context.Background(), savedCredentialInput("test-bound-password"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tamper := range []string{"ciphertext", "username", "domain"} {
		changed := ad
		changed.JoinPasswordCiphertext = slices.Clone(ad.JoinPasswordCiphertext)
		switch tamper {
		case "ciphertext":
			changed.JoinPasswordCiphertext[len(changed.JoinPasswordCiphertext)-1] ^= 1
		case "username":
			changed.JoinUsername = "differentuser"
		case "domain":
			changed.Domain = "OTHER.COM"
		}
		if _, _, err := s.ticket(context.Background(), changed, JoinInput{}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("accepted tampered %s: %v", tamper, err)
		}
	}
	if _, err := s.decryptKeytab(ad.JoinPasswordCiphertext); err == nil {
		t.Fatal("password ciphertext can be substituted for a keytab")
	}
	keytab, err := s.encryptKeytab(testKeytab("joinuser@EXAMPLE.COM", 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.decryptSecret(keytab, joinPasswordPurpose(ad)); err == nil {
		t.Fatal("keytab ciphertext can be substituted for a password")
	}
}

func TestSavedJoinCredentialsMissingEncryptionKeyCannotBeReplaced(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	ad, err := s.SaveAD(ctx, savedCredentialInput("saved-key-loss-password"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dataPath, "directory-services", "keytab.key")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveAD(ctx, savedCredentialInput("replacement-key-loss-password")); err == nil {
		t.Fatal("generated a new key over existing encrypted password state")
	}
	if _, _, err := s.ticket(ctx, ad, JoinInput{}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing saved-password key did not fail safely")
	}
	if _, err := s.SaveKeytab(ctx, 0, KeytabInput{Name: "new-keytab", File: base64.StdEncoding.EncodeToString(testKeytab("joinuser@EXAMPLE.COM", 1, 1))}); err == nil {
		t.Fatal("keytab upload regenerated a key needed by saved passwords")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("silently replaced a missing encryption key")
	}
	after, _ := s.GetAD()
	if !bytes.Equal(ad.JoinPasswordCiphertext, after.JoinPasswordCiphertext) {
		t.Fatal("key-loss error overwrote saved credentials")
	}
}

func TestSavedJoinCredentialsSwitchingToKeytabRemovesPassword(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if _, err := s.SaveAD(ctx, savedCredentialInput("password-before-keytab")); err != nil {
		t.Fatal(err)
	}
	keytab, err := s.SaveKeytab(ctx, 0, KeytabInput{Name: "join-keytab", File: base64.StdEncoding.EncodeToString(testKeytab("joinuser@EXAMPLE.COM", 1, 1))})
	if err != nil {
		t.Fatal(err)
	}
	input := savedCredentialInput("conflicting-password")
	input.KeytabID, input.KerberosPrincipal = keytab.ID, keytab.Principals[0]
	if _, err := s.SaveAD(ctx, input); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted competing password and keytab enrollment settings")
	}
	input.JoinUsername, input.JoinPassword = nil, nil
	ad, err := s.SaveAD(ctx, input)
	if err != nil || ad.JoinUsername != "" || ad.JoinPasswordConfigured || len(ad.JoinPasswordCiphertext) != 0 {
		t.Fatalf("switch to keytab retained obsolete password credentials: %v", err)
	}
	read, _ := s.GetAD()
	if read.JoinUsername != "" || read.JoinPasswordConfigured || len(read.JoinPasswordCiphertext) != 0 {
		t.Fatal("switch to keytab did not persist password removal")
	}
}

func TestSavedJoinCredentialsFailedApplyRollsBackPassword(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	password := "last-good-enrollment-password"
	if _, err := s.SaveAD(ctx, savedCredentialInput(password)); err != nil {
		t.Fatal(err)
	}
	s.run = healthyCommands(t, s, password)
	if err := s.Join(ctx, JoinInput{}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetAD()
	write := s.WriteSamba
	s.WriteSamba = func(ctx context.Context, reload bool) error {
		ad, err := s.GetAD()
		if err != nil {
			return err
		}
		if !bytes.Equal(ad.JoinPasswordCiphertext, before.JoinPasswordCiphertext) {
			return errors.New("test configuration apply failure")
		}
		return write(ctx, reload)
	}
	input := savedCredentialInput("failed-replacement-password")
	input.RealmID = before.RealmID
	if _, err := s.SaveAD(ctx, input); err == nil {
		t.Fatal("expected configuration apply failure")
	}
	after, _ := s.GetAD()
	if !after.Joined || !after.Enabled || !bytes.Equal(before.JoinPasswordCiphertext, after.JoinPasswordCiphertext) {
		t.Fatal("failed configuration apply replaced last-good saved credentials")
	}
}

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
	"os"
	"path/filepath"
	"testing"
)

func TestNFSKeytabStateTracksOnlySelectedKeysAndKerberosConfiguration(t *testing.T) {
	root := t.TempDir()
	s := &Service{keytabPath: filepath.Join(root, "krb5.keytab"), krbPath: filepath.Join(root, "krb5.conf")}
	principal := "nfs/server.example.com@EXAMPLE.COM"
	keys := testKeytab(principal, 1, 7)
	if err := os.WriteFile(s.keytabPath, keys, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.krbPath, []byte("[libdefaults]\n default_realm = EXAMPLE.COM\n"), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := s.NFSKeytabState(t.Context(), principal)
	if err != nil || len(first) != 64 {
		t.Fatalf("NFS key state: %s, %v", first, err)
	}
	merged, err := mergeKeytabs(keys, testKeytab("cifs/server.example.com@EXAMPLE.COM", 2, 8))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.keytabPath, merged, 0600); err != nil {
		t.Fatal(err)
	}
	if current, err := s.NFSKeytabState(t.Context(), principal); err != nil || current != first {
		t.Fatal("unrelated keys changed NFS identity")
	}
	for _, change := range []string{"key", "configuration"} {
		if change == "key" {
			if err := os.WriteFile(s.keytabPath, testKeytab(principal, 2, 9), 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(s.krbPath, []byte("changed configuration"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		current, err := s.NFSKeytabState(t.Context(), principal)
		if err != nil || current == first {
			t.Fatalf("%s drift ignored: %v", change, err)
		}
		first = current
	}
	if _, err := s.NFSKeytabState(t.Context(), "nfs/missing@EXAMPLE.COM"); err == nil {
		t.Fatal("missing service principal accepted")
	}
	if err := os.Chmod(s.keytabPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NFSKeytabState(t.Context(), principal); err == nil || bytes.Contains([]byte(err.Error()), bytes.Repeat([]byte{9}, 32)) {
		t.Fatal("unsafe keytab accepted or key material exposed")
	}
	if err := os.Remove(s.keytabPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(s.krbPath, s.keytabPath); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NFSKeytabState(t.Context(), principal); err == nil {
		t.Fatal("keytab symlink followed")
	}
}

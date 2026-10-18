// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func useTempOffline(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldRoot, oldRepo, oldTip := offlineRoot, offlineRepoDir, offlineTipDir
	offlineRoot, offlineRepoDir, offlineTipDir = dir, dir+"/repo", dir+"/tip"
	t.Cleanup(func() { offlineRoot, offlineRepoDir, offlineTipDir = oldRoot, oldRepo, oldTip })
	return dir
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOfflinePayloadRequiresCompleteBundle(t *testing.T) {
	dir := useTempOffline(t)
	files := []string{"repo/meta.conf", "repo/packagesite.pkg", "tip/" + tipAsset, "tip/SHA256SUMS"}
	for _, file := range files {
		writeTestFile(t, filepath.Join(dir, file), "payload")
	}
	if !OfflinePayload().Available {
		t.Fatal("complete payload must be available")
	}
	for _, file := range files {
		path := filepath.Join(dir, file)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if OfflinePayload().Available {
			t.Fatalf("payload accepted without %s", file)
		}
		writeTestFile(t, path, "payload")
	}
}

func TestBundledTip(t *testing.T) {
	useTempOffline(t)
	binary := "fake-sylve-binary"
	sum := sha256.Sum256([]byte(binary))
	digest := hex.EncodeToString(sum[:])
	writeTestFile(t, offlineTipDir+"/"+tipAsset, binary)
	writeTestFile(t, offlineTipDir+"/SHA256SUMS", "SHA256 ("+tipAsset+") = "+digest+"\n")
	writeTestFile(t, offlineTipDir+"/sylve-version.json",
		`{"version":"0.4.0","commit":"0123456789abcdef0123456789abcdef01234567"}`)

	gotDigest, version, commit, err := bundledTip()
	if err != nil {
		t.Fatal(err)
	}
	if gotDigest != digest || version != "0.4.0" || commit == "" {
		t.Fatalf("bundledTip = %s %s %s", gotDigest, version, commit)
	}

	dest := filepath.Join(t.TempDir(), "copy")
	if err := copyFileVerified(offlineTipPath(), dest, digest); err != nil {
		t.Fatalf("copyFileVerified: %v", err)
	}
	copied, err := os.ReadFile(dest)
	if err != nil || string(copied) != binary {
		t.Fatalf("copy mismatch: %q, %v", copied, err)
	}

	if err := copyFileVerified(offlineTipPath(), dest,
		"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"); err == nil {
		t.Fatal("digest mismatch must fail")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("unverified binary was left behind")
	}
}

func TestReleaseImageUsesExactBundledSylve(t *testing.T) {
	const commit = "0123456789abcdef0123456789abcdef01234567"
	const binary = "release-sylve-binary"
	sum := sha256.Sum256([]byte(binary))
	sums := fmt.Sprintf("%x  %s\n", sum, tipAsset)
	metadata := fmt.Sprintf(`{"version":"0.4.0","commit":%q}`, commit)
	oldCommit, oldTransport := releaseCommit, http.DefaultTransport
	releaseCommit = commit
	requests := 0
	transport := &http.Transport{Proxy: func(*http.Request) (*url.URL, error) {
		requests++
		return nil, fmt.Errorf("release images must not request another Sylve build")
	}}
	http.DefaultTransport = transport
	t.Cleanup(func() {
		releaseCommit, http.DefaultTransport = oldCommit, oldTransport
		transport.CloseIdleConnections()
	})

	for _, tc := range []struct {
		name, binary, sums, metadata string
		wantError                    bool
	}{
		{"exact release", binary, sums, metadata, false},
		{"different commit", binary, sums, `{"version":"0.4.0","commit":"ffffffffffffffffffffffffffffffffffffffff"}`, true},
		{"missing metadata", binary, sums, "", true},
		{"invalid metadata", binary, sums, "{", true},
		{"missing version", binary, sums, fmt.Sprintf(`{"commit":%q}`, commit), true},
		{"missing checksum", binary, "", metadata, true},
		{"missing binary", "", sums, metadata, true},
		{"corrupt binary", "corrupt", sums, metadata, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useTempOffline(t)
			for name, content := range map[string]string{
				tipAsset: tc.binary, "SHA256SUMS": tc.sums, "sylve-version.json": tc.metadata,
			} {
				if content != "" {
					writeTestFile(t, filepath.Join(offlineTipDir, name), content)
				}
			}
			dest := filepath.Join(t.TempDir(), "sylve")
			err := New(Plan{}).fetchTip(dest)
			if (err != nil) != tc.wantError {
				t.Fatalf("fetchTip() = %v, want error %v", err, tc.wantError)
			}
			if tc.wantError {
				if _, err := os.Stat(dest); !os.IsNotExist(err) {
					t.Fatal("failed release verification left a destination binary")
				}
			} else if data, err := os.ReadFile(dest); err != nil || string(data) != binary {
				t.Fatalf("installed release binary = %q, %v", data, err)
			}
			if requests != 0 {
				t.Fatal("release image tried to fetch a different Sylve binary")
			}
		})
	}
}

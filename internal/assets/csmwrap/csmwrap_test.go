// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package csmwrap

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func testPayload(t *testing.T) (Manifest, []byte, []byte) {
	t.Helper()
	data := bytes.Repeat([]byte("source-built ISO fixture"), 1024)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return Manifest{
		Architecture: "x86_64", Size: int64(len(data)),
	}, compressed.Bytes(), data
}

func TestInfoValidatesEmbeddedManifest(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("CSMWrap only available on amd64")
	}
	original := buildManifest
	t.Cleanup(func() { buildManifest = original })
	for _, failure := range []string{"", "invalid JSON", "architecture", "empty size", "oversize"} {
		t.Run(failure, func(t *testing.T) {
			manifest, _, _ := testPayload(t)
			switch failure {
			case "architecture":
				manifest.Architecture = "aarch64"
			case "empty size":
				manifest.Size = 0
			case "oversize":
				manifest.Size = maximumISOSize + 1
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "invalid JSON" {
				data = []byte("{")
			}
			buildManifest = data
			if _, err := Info(); (err != nil) != (failure != "") {
				t.Fatalf("manifest validation: %v", err)
			}
		})
	}
}

func TestMaterializeAndRefresh(t *testing.T) {
	manifest, compressed, data := testPayload(t)
	root := t.TempDir()
	path, err := materialize(root, manifest, compressed)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(contents, data) {
		t.Fatalf("extracted contents: %v", err)
	}
	stat, err := os.Stat(path)
	if err != nil || stat.Mode().Perm() != 0444 {
		t.Fatalf("permissions: %v", err)
	}
	again, err := materialize(root, manifest, compressed)
	if err != nil || again != path {
		t.Fatalf("refresh: %v", err)
	}
	if path != filepath.Join(root, "firmware", "csmwrap", "CSMWrap.iso") {
		t.Fatalf("unexpected shared media path: %s", path)
	}
}

func TestMaterializeReplacesExistingFiles(t *testing.T) {
	for _, kind := range []string{"same size", "short", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			manifest, compressed, data := testPayload(t)
			root := t.TempDir()
			dir := filepath.Join(root, "firmware", "csmwrap")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "CSMWrap.iso")
			outside := filepath.Join(t.TempDir(), "outside.iso")
			if err := os.WriteFile(outside, []byte("unrelated media"), 0644); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "same size":
				stale := bytes.Clone(data)
				stale[0] ^= 1
				err = os.WriteFile(path, stale, 0444)
			case "short":
				err = os.WriteFile(path, data[:5], 0644)
			case "symlink":
				err = os.Symlink(outside, path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := materialize(root, manifest, compressed); err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(contents, data) {
				t.Fatalf("embedded media did not replace the cached ISO: %v", err)
			}
			contents, err = os.ReadFile(outside)
			if err != nil || string(contents) != "unrelated media" {
				t.Fatalf("replacement followed a symlink: %v", err)
			}
		})
	}
}

func TestEnsureReplacesSameSizeMediaWithoutChangingOpenHandles(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("CSMWrap only available on amd64")
	}
	originalManifest, originalISO := buildManifest, compressedISO
	t.Cleanup(func() { buildManifest, compressedISO = originalManifest, originalISO })
	manifest, compressed, data := testPayload(t)
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	buildManifest, compressedISO = encoded, compressed
	root := t.TempDir()
	path, err := Ensure(root)
	if err != nil {
		t.Fatal(err)
	}
	openMedia, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer openMedia.Close()
	updated := bytes.Clone(data)
	updated[0] ^= 1
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(updated); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	compressedISO = buffer.Bytes()
	again, err := Ensure(root)
	if err != nil || again != path {
		t.Fatalf("automatic upgrade failed: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(contents, updated) {
		t.Fatalf("same-size embedded upgrade was skipped: %v", err)
	}
	contents, err = io.ReadAll(openMedia)
	if err != nil || !bytes.Equal(contents, data) {
		t.Fatalf("replacement changed already-open media: %v", err)
	}
}

func TestMaterializeFailureCleansTemporaryFiles(t *testing.T) {
	for _, failure := range []string{"short", "header", "corrupt", "truncated", "oversize"} {
		t.Run(failure, func(t *testing.T) {
			manifest, compressed, data := testPayload(t)
			root := t.TempDir()
			path, err := materialize(root, manifest, compressed)
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "short":
				manifest.Size++
			case "header":
				compressed[0] ^= 1
			case "corrupt":
				compressed[len(compressed)/2] ^= 1
			case "truncated":
				compressed = compressed[:len(compressed)-6]
			case "oversize":
				manifest.Size--
			}
			if _, err := materialize(root, manifest, compressed); err == nil {
				t.Fatal("accepted damaged payload")
			}
			files, err := os.ReadDir(filepath.Dir(path))
			if err != nil || len(files) != 1 || files[0].Name() != "CSMWrap.iso" {
				t.Fatalf("partial extraction remained: %v", err)
			}
			contents, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(contents, data) {
				t.Fatalf("failed extraction replaced the existing ISO: %v", err)
			}
		})
	}
}

func TestMaterializeConcurrentPublication(t *testing.T) {
	manifest, compressed, data := testPayload(t)
	root := t.TempDir()
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if _, err := materialize(root, manifest, compressed); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	dir := filepath.Join(root, "firmware", "csmwrap")
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 || files[0].Name() != "CSMWrap.iso" {
		t.Fatalf("temporary or duplicate images remain: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(dir, "CSMWrap.iso"))
	if err != nil || !bytes.Equal(contents, data) {
		t.Fatalf("concurrent publication corrupted the ISO: %v", err)
	}
}

func TestEmbeddedImageConcurrentExtraction(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		if _, err := Info(); err == nil {
			t.Fatal("helper available on unsupported architecture")
		}
		return
	}
	manifest, err := Info()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if _, err := Ensure(root); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	stat, err := os.Stat(filepath.Join(root, "firmware", "csmwrap", "CSMWrap.iso"))
	if err != nil || stat.Size() != manifest.Size {
		t.Fatalf("embedded ISO size: %v", err)
	}
}

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadJailCreateRequestRejectsUnknownAndMultipleDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(path, []byte(`{"name":"jail","unexpected":true}`), 0600); err != nil {
		t.Fatalf("write unknown-field request: %v", err)
	}
	if _, err := LoadJailCreateRequest(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}

	if err := os.WriteFile(path, []byte(`{} {}`), 0600); err != nil {
		t.Fatalf("write multiple-documents request: %v", err)
	}
	if _, err := LoadJailCreateRequest(path); err == nil || !strings.Contains(err.Error(), "more than one JSON value") {
		t.Fatalf("multiple-documents error = %v", err)
	}
}

func TestJailCreateConsoleAcceptsOneTypedZFSSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zfs-copy.json")
	if err := os.WriteFile(path, []byte(`{"name":"copied-jail","ctId":42,"pool":"destination","switchName":"none","type":"freebsd","zfsSource":{"dataset":"source/root","guid":"123"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	request, err := BuildJailCreateRequest(path, JailCreateOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if request.SourceCount() != 1 || request.ZFSSource == nil || request.ZFSSource.Dataset != "source/root" || request.ZFSSource.GUID != "123" {
		t.Fatalf("source contract lost: %+v", request)
	}
	base := "mixed-base"
	if _, err := BuildJailCreateRequest(path, JailCreateOverrides{Base: &base}); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("mixed sources accepted: %v", err)
	}
}

func TestJailCreateConsoleRejectsSnapshotSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot-source.json")
	for _, field := range []string{`"snapshot":"selected"`, `"snapshot":""`, `"snapshotGuid":"123"`, `"snapshotGuid":null`} {
		payload := `{"name":"copied-jail","ctId":42,"pool":"destination","switchName":"none","type":"freebsd","zfsSource":{"dataset":"source/root","guid":"123",` + field + `}}`
		if err := os.WriteFile(path, []byte(payload), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := BuildJailCreateRequest(path, JailCreateOverrides{}); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("snapshot source accepted: %s: %v", field, err)
		}
	}
}

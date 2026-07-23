package console

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
)

func TestLoadVMCreateRequest(t *testing.T) {
	rid := uint(701)
	want := libvirtServiceInterfaces.CreateVMRequest{
		Name:        "vm-file-request",
		RID:         &rid,
		StorageType: libvirtServiceInterfaces.StorageTypeNone,
		TimeOffset:  libvirtServiceInterfaces.TimeOffsetUTC,
	}
	contents, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	path := filepath.Join(t.TempDir(), "vm.json")
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatalf("write request: %v", err)
	}

	got, err := LoadVMCreateRequest(path)
	if err != nil {
		t.Fatalf("load request: %v", err)
	}
	if got.RID == nil || *got.RID != rid || got.Name != want.Name || got.StorageType != want.StorageType {
		t.Fatalf("loaded request = %#v", got)
	}
}

func TestLoadVMCreateRequestRejectsUnknownAndMultipleDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(path, []byte(`{"name":"vm","rid":701,"unexpected":true}`), 0600); err != nil {
		t.Fatalf("write unknown-field request: %v", err)
	}
	if _, err := LoadVMCreateRequest(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}

	if err := os.WriteFile(path, []byte(`{} {}`), 0600); err != nil {
		t.Fatalf("write multiple-documents request: %v", err)
	}
	if _, err := LoadVMCreateRequest(path); err == nil || !strings.Contains(err.Error(), "more than one JSON value") {
		t.Fatalf("multiple-documents error = %v", err)
	}
}

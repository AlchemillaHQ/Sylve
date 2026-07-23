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

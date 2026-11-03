package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	validKeyA = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIM08Gi2EW4Wpd4QEKRmIBwbhOjXA+HaDhqSaRTDe2W3N test"
	validKeyB = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDNYnmY6+HQr2w0oZXa8bFU9Izm5cHfIjNBXdmm62HfwbIRyOeN3G8lYH2/dj6KdExasgTXipc2Rbb+x+1B4vVhnzKo5JLIpIcm1Ca/CM2Rr0Bwl5LREOjCX0wzT8rdcNWTcvS2CpcBbuxdkWS7EQz4tMoD8q/UGMSTmbEoFRQ9eOT0xBos8CX+J5ovCL6nonwPojCxmwvjgkjSTiWXxbwc8Be2HI3YQqX8GsEODS3wp7V7Zee2rloxm89AjKYSiJMeOWIlTuRJqHUCsy1WOYTKwKxS05fQqeM0onzX2dBzhKhaXKkUbAqEGE+ZzzVOPjbShu8a2eoFmQnVDIFeZYaR pwn"
)

func TestWriteSSHAuthorizedKeyRejectsInjection(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		errContains string
	}{
		{name: "empty key", key: "", errContains: "no key found"},
		{name: "garbage input", key: "not a key", errContains: "invalid SSH public key"},
		{name: "command= option in single line", key: `command="/usr/bin/true" ` + validKeyB, errContains: "options are not allowed"},
		{name: "two-line injection", key: validKeyA + "\n" + `command="echo x" ` + validKeyB, errContains: "multiple keys are not allowed"},
		{name: "two plain keys", key: validKeyA + "\n" + validKeyB, errContains: "multiple keys are not allowed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			err := WriteSSHAuthorizedKey(home, tc.key)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Fatalf("error %v does not contain %q", err, tc.errContains)
			}
			if _, err := os.Stat(filepath.Join(home, ".ssh", "authorized_keys")); err == nil {
				t.Fatalf("authorized_keys must not be created on failure")
			}
		})
	}
}

func TestWriteSSHAuthorizedKeyWritesValidKeys(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "ed25519", key: validKeyA},
		{name: "rsa", key: validKeyB},
		{name: "trims surrounding whitespace", key: "  " + validKeyA + "\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if err := WriteSSHAuthorizedKey(home, tc.key); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
			if err != nil {
				t.Fatalf("failed to read authorized_keys: %v", err)
			}
			want := strings.TrimSpace(tc.key) + "\n"
			if string(got) != want {
				t.Fatalf("file = %q, want %q", got, want)
			}
		})
	}
}

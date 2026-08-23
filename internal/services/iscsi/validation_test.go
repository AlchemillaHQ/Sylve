// SPDX-License-Identifier: BSD-2-Clause

package iscsi

import (
	"errors"
	"testing"
)

func TestNormalizeInitiatorTargetAddress(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "IPv4", input: "192.0.2.10", want: "192.0.2.10"},
		{name: "raw IPv6", input: "2001:db8::10", want: "[2001:db8::10]"},
		{name: "IPv6 with port", input: "[2001:db8::10]:3261", want: "[2001:db8::10]:3261"},
		{name: "hostname", input: "Storage.Example.COM", want: "storage.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeInitiatorTargetAddress(tt.input)
			if err != nil {
				t.Fatalf("normalizeInitiatorTargetAddress(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeInitiatorTargetAddress(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizePortalAddress(t *testing.T) {
	got, err := normalizePortalAddress("2001:db8::10")
	if err != nil {
		t.Fatalf("normalize IPv6 portal: %v", err)
	}
	if got != "[2001:db8::10]" {
		t.Fatalf("normalized IPv6 portal = %q", got)
	}

	for _, input := range []string{"storage.example.com", "192.0.2.10:3260"} {
		if _, err := normalizePortalAddress(input); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected %q to be rejected, got %v", input, err)
		}
	}
}

func TestConfigTokensRejectInjectionCharacters(t *testing.T) {
	for _, input := range []string{"name\nlisten 0.0.0.0", "name {", "name;", "name#comment"} {
		if err := validateBareConfigToken(input, "target_name", maxISCSINameLength); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected %q to be rejected, got %v", input, err)
		}
	}
}

func TestValidateZVol(t *testing.T) {
	if err := validateZVol("tank/vm/volume-0"); err != nil {
		t.Fatalf("valid zvol rejected: %v", err)
	}

	for _, input := range []string{"tank", "/tank/volume", "tank/../volume", "tank/volume\npath /dev/null", "tank/volume@snapshot"} {
		if err := validateZVol(input); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected %q to be rejected, got %v", input, err)
		}
	}
}

func TestValidateChapSecretRejectsControlCharacters(t *testing.T) {
	err := validateChapSecret("12345678901\n", "chap_secret")
	if !errors.Is(err, ErrInvalidRequest) || err.Error() != "chap_secret_contains_invalid_characters" {
		t.Fatalf("expected invalid CHAP secret, got %v", err)
	}
}

func TestMutationMethodsRejectConfigInjection(t *testing.T) {
	t.Run("initiator nickname", func(t *testing.T) {
		svc := newInitiatorTestService(t)
		err := svc.CreateInitiator("bad\nsection", "192.0.2.10", "iqn.2025-01.com.example:target0", "", "None", "", "", "", "")
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected invalid nickname, got %v", err)
		}
	})

	t.Run("target name", func(t *testing.T) {
		svc := newTargetTestService(t)
		err := svc.CreateTarget("iqn.2025-01.com.example:target0 {", "", "None", "", "", "", "")
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected invalid target name, got %v", err)
		}
	})

	t.Run("portal hostname", func(t *testing.T) {
		svc := newTargetTestService(t)
		err := svc.AddPortal(1, "storage.example.com", 3260)
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected invalid portal address, got %v", err)
		}
	})

	t.Run("zvol path", func(t *testing.T) {
		svc := newTargetTestService(t)
		err := svc.AddLUN(1, 0, "tank/volume\npath /dev/null")
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected invalid zvol, got %v", err)
		}
	})
}

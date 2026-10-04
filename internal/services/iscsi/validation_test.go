// SPDX-License-Identifier: BSD-2-Clause

package iscsi

import (
	"errors"
	"net"
	"testing"

	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
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

func TestPortalAddressValidation(t *testing.T) {
	svc := &Service{}
	got, err := svc.endpoint("2001:db8::10", 3260)
	if err != nil {
		t.Fatalf("normalize IPv6 portal: %v", err)
	}
	if got.listen() != "[2001:db8::10]:3260" {
		t.Fatalf("normalized IPv6 portal = %q", got.listen())
	}

	for _, input := range []string{"storage.example.com", "192.0.2.10:3260"} {
		if _, err := svc.endpoint(input, 3260); !errors.Is(err, ErrInvalidRequest) {
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

func TestPortalEndpointCanonicalization(t *testing.T) {
	svc := newTargetTestService(t)
	svc.interfaceLookup = func(name string) (*net.Interface, error) {
		if name == "em0" || name == "alias0" {
			return &net.Interface{Name: "em0", Index: 2}, nil
		}
		return nil, errors.New("unknown interface")
	}
	for _, test := range []struct {
		input string
		want  string
	}{
		{"127.0.0.1", "127.0.0.1:3260"},
		{"[2001:0db8::0001]", "[2001:db8::1]:3260"},
		{"fe80::1%em0", "[fe80::1%em0]:3260"},
		{"fe80::1%alias0", "[fe80::1%em0]:3260"},
	} {
		endpoint, err := svc.endpoint(test.input, 0)
		if err != nil || endpoint.listen() != test.want {
			t.Fatalf("%s: listener=%s error=%v", test.input, endpoint.listen(), err)
		}
	}
	for _, input := range []string{"::ffff:127.0.0.1", "fe80::1", "fe80::1%2", "fe80::1%unknown", "fe80::1%bad/name", "::%em0", "2001:db8::1%em0", "::1%em0"} {
		if _, err := svc.endpoint(input, 3260); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s: error=%v", input, err)
		}
	}
}

func TestPortalOverlapAndSharing(t *testing.T) {
	for _, test := range []struct {
		name     string
		first    string
		second   string
		v6Only   bool
		conflict bool
	}{
		{"IPv4 wildcard", "0.0.0.0", "127.0.0.1", true, true},
		{"IPv6 wildcard", "::", "::1", true, true},
		{"separate stacks", "::", "127.0.0.1", true, false},
		{"dual stack", "::", "127.0.0.1", false, true},
		{"shared exact", "127.0.0.1", "127.0.0.1", true, false},
		{"equivalent IPv6", "::1", "[0:0:0:0:0:0:0:1]", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := newTargetTestService(t)
			svc.ipv6Only = func() bool { return test.v6Only }
			targets := []iscsiModels.ISCSITarget{{ID: 1, Portals: []iscsiModels.ISCSITargetPortal{{Address: test.first, Port: 3260}}}, {ID: 2, Portals: []iscsiModels.ISCSITargetPortal{{Address: test.second, Port: 3260}}}}
			endpoints, _, err := svc.collectPortalEndpoints(targets)
			if errors.Is(err, ErrConflict) != test.conflict || err != nil && !test.conflict {
				t.Fatalf("error=%v", err)
			}
			if !test.conflict && test.name == "shared exact" && len(endpoints) != 1 {
				t.Fatal("shared endpoint not deduplicated")
			}
		})
	}
}

func TestPortalScopeIdentityCannotCreateCompetingGroups(t *testing.T) {
	svc := newTargetTestService(t)
	svc.interfaceLookup = func(name string) (*net.Interface, error) { return &net.Interface{Name: name, Index: 2}, nil }
	targets := []iscsiModels.ISCSITarget{{ID: 1, Portals: []iscsiModels.ISCSITargetPortal{{Address: "fe80::1%em0", Port: 3260}}}, {ID: 2, Portals: []iscsiModels.ISCSITargetPortal{{Address: "fe80::1%alias0", Port: 3260}}}}
	if _, _, err := svc.collectPortalEndpoints(targets); !errors.Is(err, ErrConflict) {
		t.Fatalf("same resolved scope created competing groups: %v", err)
	}
}

func TestPortalGroupNamesUseStableCanonicalKeys(t *testing.T) {
	svc := newTargetTestService(t)
	endpoint, err := svc.endpoint("127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.key() != "tcp|v4|9:127.0.0.1|3260" {
		t.Fatalf("key=%s", endpoint.key())
	}
	name := svc.groupName(endpoint)
	other, err := svc.endpoint("::1", 3260)
	if err != nil {
		t.Fatal(err)
	}
	if other.key() != "tcp|v6|3:::1|3260" || name == svc.groupName(other) || svc.groupName(endpoint) != name {
		t.Fatal("unstable endpoint naming")
	}
}

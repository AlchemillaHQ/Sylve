// SPDX-License-Identifier: BSD-2-Clause

package iscsi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"slices"
	"strings"
	"testing"
)

type nativeGrammarFixture struct {
	Revision     string   `json:"revision"`
	LexerSHA256  string   `json:"lexerSHA256"`
	ParserSHA256 string   `json:"parserSHA256"`
	Keywords     string   `json:"keywords"`
	Accepted     []string `json:"accepted"`
	Rejected     []string `json:"rejected"`
}

func loadNativeGrammarFixture(t *testing.T) nativeGrammarFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/native-grammar.json")
	var fixture nativeGrammarFixture
	if err != nil || json.Unmarshal(data, &fixture) != nil {
		t.Fatal("cannot read pinned native grammar fixture")
	}
	return fixture
}

func TestExtraConfigPinnedNativeGrammar(t *testing.T) {
	fixture := loadNativeGrammarFixture(t)
	if fixture.Revision != nativeGrammarRevision || len(fixture.LexerSHA256) != 64 || len(fixture.ParserSHA256) != 64 || !slices.Equal(strings.Fields(fixture.Keywords), nativeKeywords) {
		t.Fatal("native grammar revision or keyword set changed")
	}
	svc := newTargetTestService(t)
	for _, text := range fixture.Accepted {
		if _, _, err := svc.parseExtraTargetConfig(t.Context(), text, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range fixture.Rejected {
		if _, _, err := svc.parseExtraTargetConfig(t.Context(), text, nil); !errors.Is(err, ErrInvalidExtraConfig) {
			t.Fatal("unsupported native fixture was accepted")
		}
	}
}

func TestExtraConfigNativeIdentityAndObservableOptions(t *testing.T) {
	svc := newTargetTestService(t)
	text := `portal-group user { listen 127.0.0.1:49222 option note native } target IQN.USER { portal-group user lun 0 { backend ramdisk size 64K option vendor SYLVE } }`
	state, rendered, err := svc.parseExtraTargetConfig(t.Context(), text, nil)
	if err != nil || state.targets[0].name != "iqn.user" || state.targets[0].luns[0].name != "iqn.user,lun,0" || state.targets[0].luns[0].options["vendor"] != "SYLVE" || !strings.Contains(rendered, "target IQN.USER") {
		t.Fatal("native identity or rendered token preservation failed")
	}
	for _, text := range []string{
		`lun user { backend ramdisk size 64K option ctld_name foreign }`,
		`lun user { backend ramdisk size 64K option "size" 128 }`,
		`lun user { path /dev/null option num_threads 1 }`,
		`lun user { path relative.img }`,
		`lun user { backend ramdisk size 64K option vendor "<injection>" }`,
		`lun user { backend ramdisk size 64K serial "` + strings.Repeat("S", 17) + `" }`,
		`lun user { backend ramdisk size 64K device-id "` + strings.Repeat("D", 65) + `" }`,
		`portal-group user { option subnqn foreign }`,
		`portal-group user { option port_name foreign }`,
		`portal-group user { option host foreign }`,
		`portal-group user { option "invalid name" foreign }`,
		`portal-group "unsafe&name" {}`,
	} {
		if _, _, err := svc.parseExtraTargetConfig(t.Context(), text, nil); !errors.Is(err, ErrInvalidExtraConfig) {
			t.Fatal("unobservable or truncated property accepted")
		}
	}
}

func TestExtraConfigParserPreservesTextAndCanonicalizesOnlyListeners(t *testing.T) {
	svc := newTargetTestService(t)
	svc.interfaceLookup = func(string) (*net.Interface, error) { return &net.Interface{Name: "em0", Index: 2}, nil }
	text := `# listen and { } are comments
auth-group user-auth { chap "user # {} listen" "literal\backslash"; }
portal-group user-pg { discovery-auth-group no-authentication; listen "[0:0:0:0:0:0:0:1]"; option note "listen # {}" }
portal-group scoped { listen "[fe80::1%em0]:3261" }
transport-group user-tg { listen tcp 127.0.0.1; listen discovery-tcp "[::1]" }
lun named { backend ramdisk; size 1M; blocksize 4096; serial "owned"; device-id "id"; ctl-lun 20; device-type disk; option vendor "Sylve" }
target "iqn.2026-10.test:user" { alias "quoted listen { } #"; portal-group user-pg user-auth; auth-group user-auth; lun 0 named; }
controller "nqn.2026-10.io.sylve:user" { transport-group user-tg; auth-type none; namespace 1 named }
`
	state, rendered, err := svc.parseExtraTargetConfig(t.Context(), text, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(text, `listen "[0:0:0:0:0:0:0:1]"`, `listen "[::1]:3260"`, 1)
	want = strings.Replace(want, "listen tcp 127.0.0.1;", `listen tcp "127.0.0.1:4420";`, 1)
	want = strings.Replace(want, `listen discovery-tcp "[::1]"`, `listen discovery-tcp "[::1]:8009"`, 1)
	if rendered != want {
		t.Fatal("rendering changed a non-listener token or missed a listener")
	}
	if len(state.configuredLUNs()) != 1 || len(state.activeEndpoints()) != 3 || len(state.groups) != 3 || len(state.targets) != 2 {
		t.Fatal("wrong reserved, active, target, or LUN topology")
	}
	if state.targets[1].luns[0].number != 1 || state.targets[1].luns[0].name != "named" || state.luns[0].size != 1<<20 || *state.luns[0].ctlID != 20 {
		t.Fatal("named namespace properties were lost")
	}
}

func TestExtraConfigParserRejectsUnmodeledSyntax(t *testing.T) {
	svc := newTargetTestService(t)
	for _, text := range []string{
		`pidfile /tmp/secret`, `debug 3`, `timeout 10`, `maxproc 2`, `isns-server 127.0.0.1`, `include /tmp/config`,
		`target user {}`, `controller user {}`, `portal-group default {}`, `transport-group PG-user {}`, `auth-group AG-user {}`,
		`auth-group No-Authentication {}`, `auth-group no-access {}`, `auth-group default {}`,
		`portal-group user { listen-iser 127.0.0.1 }`, `portal-group user { foreign }`, `portal-group user { offload cxgbe0 }`,
		`portal-group user { tag 1 }`, `portal-group user { unknown value }`, `transport-group user { listen udp 127.0.0.1 }`,
		`portal-group user {} target user { portal-group user port 1 }`, `portal-group user {} target user { portal-group pg-unbound }`,
		`portal-group user {} target user { portal-group default }`, `portal-group user {} target user { portal-group user auth-group ag-1 }`,
		`target user { portal-group later } portal-group later {}`, `portal-group user { discovery-auth-group missing }`,
		`portal-group user { listen "hostname.example:3260" }`, `portal-group user { listen "[::ffff:127.0.0.1]:3260" }`,
		`portal-group user { listen "[fe80::1%2]:3260" }`, `portal-group user { listen "[::1%em0]:3260" }`,
		`auth-group user { chap "" "value" }`, "auth-group user { chap \"line\nline\" value }", "auth-group user { chap \"line\rline\" value }",
		`auth-group user { chap "x\" listen 127.0.0.1:3260 #" }`, `auth-group user { chap "user" "trailing\" }`,
		`auth-group user { chap listen value }`, `auth-group user { chap username "secret"; ; }`,
		`lun user { backend other size 1M }`, `lun user { backend block }`, `lun user { backend ramdisk }`,
		`lun user { backend ramdisk path /dev/null size 1M }`, `lun user { backend ramdisk size 1M size 2M }`,
		`transport-group user {} controller user { transport-group user namespace 0 { backend ramdisk size 1M } }`,
		`portal-group user {} target user { portal-group user lun 0 missing }`,
		`portal-group user {} target user { portal-group user lun 1024 { backend ramdisk size 1M } }`,
		`portal-group user {} target user { portal-group user lun 0 { backend ramdisk size 1M } lun 0 { backend ramdisk size 1M } }`,
		`auth-group user { chap "secret` + string([]byte{0}) + `" value }`, `# ` + string([]byte{0}), string([]byte{0xff}),
	} {
		if _, _, err := svc.parseExtraTargetConfig(t.Context(), text, nil); !errors.Is(err, ErrInvalidExtraConfig) {
			t.Fatalf("unsafe syntax accepted (length %d): %v", len(text), err)
		}
	}
}

func TestExtraConfigParserSeparatesReservationsFromActivation(t *testing.T) {
	svc := newTargetTestService(t)
	text := `portal-group unused { listen 127.0.0.1:49222 }
portal-group redirecting { listen 127.0.0.1:49223 redirect 127.0.0.1:49224 }
portal-group empty {} target user { portal-group empty lun 0 { backend ramdisk size 1M } }
lun unattached { backend ramdisk size 1M }`
	state, _, err := svc.parseExtraTargetConfig(t.Context(), text, nil)
	if err != nil || len(state.activeEndpoints()) != 1 || len(state.configuredLUNs()) != 2 {
		t.Fatalf("activation rules: %v", err)
	}
	managed := &targetConfiguration{groups: map[string]targetGroup{"managed": state.groups["unused"]}, targets: []targetDefinition{{name: "iqn.managed", groups: []string{"managed"}}}}
	if _, _, err := svc.parseExtraTargetConfig(t.Context(), text, managed); !errors.Is(err, ErrInvalidExtraConfig) {
		t.Fatal("unused extra listener did not reserve its endpoint")
	}
}

func TestExtraConfigParserChecksNamesAndCrossProtocolOverlaps(t *testing.T) {
	svc := newTargetTestService(t)
	managed := &targetConfiguration{groups: make(map[string]targetGroup), targets: []targetDefinition{{name: "IQN.MANAGED"}}}
	for _, text := range []string{
		`portal-group user {} target iqn.managed { portal-group user }`,
		`transport-group user {} controller iqn.managed { transport-group user }`,
		`portal-group user { listen 127.0.0.1:4420 } transport-group other { listen tcp 127.0.0.1 }`,
		`transport-group user { listen tcp 127.0.0.1 listen tcp 127.0.0.1:4420 }`,
		`transport-group user { listen discovery-tcp 0.0.0.0 } portal-group other { listen 127.0.0.1:8009 }`,
	} {
		if _, _, err := svc.parseExtraTargetConfig(t.Context(), text, managed); !errors.Is(err, ErrInvalidExtraConfig) {
			t.Fatal("name or endpoint collision accepted")
		}
	}
	text := `portal-group user { listen "[::]:4420" } transport-group other { listen tcp 127.0.0.1 }`
	for _, v6only := range []bool{true, false} {
		svc.ipv6Only = func() bool { return v6only }
		_, _, err := svc.parseExtraTargetConfig(t.Context(), text, nil)
		if (err == nil) != v6only {
			t.Fatal("cross-family wildcard policy not enforced")
		}
	}
}

func TestExtraConfigParserRejectsOverlapWhenIPv6PolicyUnavailable(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
		failed bool
	}{
		{name: "read failed", failed: true},
		{name: "invalid output", output: "unknown"},
		{name: "failed read with output", output: "1", failed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := newTargetTestService(t)
			svc.ipv6Only = nil
			reads := 0
			svc.runtime.run = func(_ context.Context, _, command string, args ...string) (string, error) {
				if command != "/sbin/sysctl" || !slices.Equal(args, []string{"-n", "net.inet6.ip6.v6only"}) {
					t.Fatal("unexpected command during IPv6 policy check")
				}
				reads++
				if test.failed {
					return test.output, errors.New("unreadable IPv6 policy")
				}
				return test.output, nil
			}
			text := `portal-group user { listen "[::]:4420" } transport-group other { listen tcp 127.0.0.1 }`
			_, _, err := svc.parseExtraTargetConfig(t.Context(), text, nil)
			var detail *ExtraConfigError
			if !errors.As(err, &detail) || detail.ReasonCode != "extra_config_listener_overlap" || reads != 1 {
				t.Fatal("unavailable IPv6 policy allowed cross-family wildcard overlap")
			}
		})
	}
}

func TestExtraConfigParserBoundsFieldAndReportsOnlyPhysicalLine(t *testing.T) {
	svc := newTargetTestService(t)
	_, _, err := svc.parseExtraTargetConfig(t.Context(), strings.Repeat(" ", MaxExtraConfigBytes+1), nil)
	if !errors.Is(err, ErrExtraConfigTooLarge) {
		t.Fatal("field limit was bypassed")
	}
	_, _, err = svc.parseExtraTargetConfig(t.Context(), "# comment\r\n\nportal-group user {\n listen \"credential-near-token\"\n}", nil)
	var detail *ExtraConfigError
	if !errors.As(err, &detail) || detail.LineNumber == nil || *detail.LineNumber != 4 || strings.Contains(err.Error(), "credential") {
		t.Fatal("source line or redaction failed")
	}
}

func TestExtraConfigNativeNumbers(t *testing.T) {
	for text, want := range map[string]uint64{"0": 0, "512": 512, "1K": 1024, "2m": 2 << 20, "0x1000": 4096, "010": 8} {
		got, err := nativeConfigNumber(text)
		if err != nil || got != want {
			t.Fatalf("number %s=%d error=%v", text, got, err)
		}
	}
	for _, text := range []string{"", "-1", "09", "1_000", "1MiB", "999E"} {
		if _, err := nativeConfigNumber(text); err == nil {
			t.Fatal("non-native numeric syntax accepted")
		}
	}
}

// SPDX-License-Identifier: BSD-2-Clause

package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/gin-gonic/gin/binding"
)

func TestFirewallTrafficOptionRendering(t *testing.T) {
	for _, test := range []struct{ action, state, response, expected string }{
		{"pass", "default", "default", " to any label"},
		{"pass", "keep", "default", " to any keep state label"},
		{"pass", "none", "default", " to any no state label"},
		{"block", "default", "default", "block in"},
		{"block", "default", "drop", "block drop in"},
		{"block", "default", "return", "block return in"},
	} {
		t.Run(test.action+test.state+test.response, func(t *testing.T) {
			rule := networkModels.FirewallTrafficRule{ID: 1, Name: "test", Enabled: true, Action: test.action,
				Direction: "in", Protocol: "tcp", Family: "inet", StatePolicy: test.state, BlockResponse: test.response}
			text, err := (&Service{}).renderTrafficRules([]networkModels.FirewallTrafficRule{rule}, nil)
			if err != nil || !strings.Contains(text, test.expected) {
				t.Fatalf("render: text=%s err=%v", text, err)
			}
			if test.state == "default" && (strings.Contains(text, " keep state") || strings.Contains(text, " no state")) {
				t.Fatalf("default must not emit a state clause: %s", text)
			}
		})
	}
}

func TestFirewallICMPTypesAndValidation(t *testing.T) {
	for _, protocol := range []string{"icmp", "icmp6"} {
		rule := networkModels.FirewallTrafficRule{ID: 3, Name: protocol, Enabled: true, Action: "pass",
			Direction: "in", Family: "any", Protocol: protocol, ICMPTypes: []string{"echoreq", "echorep"}}
		text, err := (&Service{}).renderTrafficRules([]networkModels.FirewallTrafficRule{rule}, nil)
		clause := " icmp-type "
		if protocol == "icmp6" {
			clause = " icmp6-type "
		}
		if err != nil || !strings.Contains(text, clause+"{ echoreq, echorep }") {
			t.Fatalf("protocol=%s: text=%s err=%v", protocol, text, err)
		}
	}
	for _, change := range []func(*networkServiceInterfaces.UpsertFirewallTrafficRuleRequest){
		func(r *networkServiceInterfaces.UpsertFirewallTrafficRuleRequest) {
			r.StatePolicy = "none"
			r.Action = "block"
		},
		func(r *networkServiceInterfaces.UpsertFirewallTrafficRuleRequest) { r.BlockResponse = "return" },
		func(r *networkServiceInterfaces.UpsertFirewallTrafficRuleRequest) {
			r.ICMPTypes = []string{"echoreq"}
			r.Protocol = "tcp"
		},
		func(r *networkServiceInterfaces.UpsertFirewallTrafficRuleRequest) {
			r.ICMPTypes = []string{"neighbrsol"}
		},
		func(r *networkServiceInterfaces.UpsertFirewallTrafficRuleRequest) {
			r.ICMPTypes = []string{"echoreq", "echoreq"}
		},
		func(r *networkServiceInterfaces.UpsertFirewallTrafficRuleRequest) {
			r.Protocol = "icmp6"
			r.Family = "inet"
		},
	} {
		r := networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{Name: "invalid", Action: "pass", Direction: "in", Protocol: "icmp", Family: "any"}
		change(&r)
		if err := (&Service{}).validateFirewallTrafficRuleRequest(&r); !errors.Is(err, ErrInvalidFirewallTrafficRule) {
			t.Fatalf("expected invalid options for %+v, got %v", r, err)
		}
	}
}

func TestFirewallAdvancedPFValidationAndBindings(t *testing.T) {
	r := networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{Name: "sshguard", Kind: "advanced", RawPF: "# comment\nblock in from <sshguard> to any\n"}
	if err := binding.Validator.ValidateStruct(&r); err != nil {
		t.Fatalf("raw API request must not require structured fields: %v", err)
	}
	if err := (&Service{}).validateFirewallTrafficRuleRequest(&r); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "# only a comment", "set skip on lo0", "table <x> persist", "include \"/etc/pf.conf\"",
		"nat on em0 from any to any -> (em0)", "pass all\nload anchor \"x\" from \"/etc/pf.conf\"",
		"pass all \\", "pass all label \"unterminated", "pass all\x00\n", "pass all\nanchor \"x\" {\npass all\n}",
		"pass in from any to any rdr-to 10.0.0.10", "match out nat-to(em0)", "pass out binat-to 10.0.0.10",
		"pass all \\\n}\n", "pass on { em0"} {
		if err := validateFirewallRawPF(raw); err == nil {
			t.Errorf("unsafe/non-filter snippet accepted: %q", raw)
		}
	}
	if err := validateFirewallRawPF("pass in on igb1.3 \\\n    from 10.32.50.0/24 to any no state\nblock in from <sshguard>\n"); err != nil {
		t.Fatalf("continuation and multiple filter rules should work: %v", err)
	}
	if err := validateFirewallRawPF("pass all label \"rdr-to # { ;\" # nat-to }\n"); err != nil {
		t.Fatalf("quoted labels and comments must not be treated as PF options: %v", err)
	}
	r.Action = "pass"
	if err := (&Service{}).validateFirewallTrafficRuleRequest(&r); err == nil {
		t.Fatal("raw rows must reject structured fields")
	}
}

func TestFirewallAdvancedPFRendersInSharedOrderUnchanged(t *testing.T) {
	raw := "# sshguard\nblock in from <sshguard> to any\n"
	rules := []networkModels.FirewallTrafficRule{
		{ID: 3, Name: "later", Priority: 3, Enabled: true, Action: "block", Direction: "in", Protocol: "tcp", Family: "inet", BlockResponse: "return"},
		{ID: 2, Name: "SSHGuard", Priority: 2, Enabled: true, Kind: "advanced", RawPF: raw},
		{ID: 1, Name: "LAN", Priority: 1, Enabled: true, Action: "pass", Direction: "in", Protocol: "any", Family: "inet", StatePolicy: "none"},
		{ID: 4, Priority: 4, Kind: "advanced", RawPF: "set skip on lo0", Enabled: false},
	}
	text, err := (&Service{}).renderTrafficRules(rules, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, snippet, last := strings.Index(text, `label "sylve_trf_1"`), strings.Index(text, raw), strings.Index(text, `label "sylve_trf_3"`)
	if first < 0 || snippet <= first || last <= snippet || strings.Contains(text, "sylve_trf_2") || strings.Contains(text, "set skip") {
		t.Fatalf("wrong shared order or rewritten raw block:\n%s", text)
	}
}

func TestFirewallCombinedTrafficStreamUsesMainMacroContextAndPreservesWrapperOrder(t *testing.T) {
	text := buildPFMainConfig("lan_net=\"10.32.50.0/24\"\nset block-policy return", "", "", "block in all", "pass out all", "", "", "/tmp/nat-rules.conf", "/tmp/traffic-rules.conf")
	stream := "anchor \"sylve\" {\nanchor \"traffic-rules\" {\ninclude \"/tmp/traffic-rules.conf\"\n}\n}\n"
	if !strings.Contains(text, stream) || strings.Contains(text, `load anchor "sylve/traffic-rules"`) ||
		strings.Contains(text, `anchor "sylve" quick`) || strings.Contains(text, `anchor "traffic-rules" quick`) {
		t.Fatalf("stream is separately parsed, renamed, or made top-level quick:\n%s", text)
	}
	if strings.Index(text, "block in all") > strings.Index(text, stream) || strings.Index(text, "pass out all") < strings.Index(text, stream) {
		t.Fatalf("surrounding top-level Advanced filtering order changed:\n%s", text)
	}
}

func TestFirewallCreatePreservesExplicitDisabledFlag(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.FirewallTrafficRule{}, &networkModels.FirewallNATRule{})
	if err := db.Create(&models.BasicSettings{}).Error; err != nil {
		t.Fatal(err)
	}
	for _, req := range []networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{
		{Name: "disabled-standard", Action: "pass", Direction: "in", Protocol: "tcp", Family: "inet", Enabled: boolPtr(false)},
		{Name: "disabled-raw", Kind: "advanced", RawPF: "block in from <sshguard>\n", Enabled: boolPtr(false)},
	} {
		id, err := svc.CreateFirewallTrafficRule(&req)
		if err != nil {
			t.Fatal(err)
		}
		var rule networkModels.FirewallTrafficRule
		if err := db.First(&rule, id).Error; err != nil || rule.Enabled {
			t.Fatalf("explicitly disabled rule created enabled: %+v err=%v", rule, err)
		}
		if text, err := svc.renderTrafficRules([]networkModels.FirewallTrafficRule{rule}, nil); err != nil || strings.Contains(text, "pass") || strings.Contains(text, "block") {
			t.Fatalf("disabled row was rendered: text=%s err=%v", text, err)
		}
	}
	req := validFirewallNATRuleRequest("disabled-NAT")
	req.Enabled = boolPtr(false)
	id, err := svc.CreateFirewallNATRule(&req)
	if err != nil {
		t.Fatal(err)
	}
	var nat networkModels.FirewallNATRule
	if err := db.First(&nat, id).Error; err != nil || nat.Enabled {
		t.Fatalf("explicitly disabled NAT created enabled: %+v err=%v", nat, err)
	}
}

func TestFirewallMixedRowsShareOrderingAndHiddenRuleProtection(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.FirewallTrafficRule{})
	if err := db.Create(&models.BasicSettings{}).Error; err != nil {
		t.Fatal(err)
	}
	hidden := networkModels.FirewallTrafficRule{Name: "WireGuard generated", Priority: 7, Action: "pass", Direction: "in", Family: "inet"}
	if err := db.Create(&hidden).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&hidden).UpdateColumn("visible", false).Error; err != nil {
		t.Fatal(err)
	}
	raw := "# friends exception\npass in quick from <friends> to any\n"
	priority := 1
	advancedID, err := svc.CreateFirewallTrafficRule(&networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{
		Name: "friends", Kind: "advanced", RawPF: raw, Priority: &priority,
	})
	if err != nil {
		t.Fatal(err)
	}
	standardID, err := svc.CreateFirewallTrafficRule(&networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{
		Name: "baseline", Action: "pass", Direction: "in", Family: "inet", Protocol: "any", StatePolicy: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ReorderFirewallTrafficRules([]networkServiceInterfaces.FirewallReorderRequest{
		{ID: standardID, Priority: 1}, {ID: advancedID, Priority: 2},
	}); err != nil {
		t.Fatal(err)
	}
	rules, err := svc.GetFirewallTrafficRules()
	if err != nil || len(rules) != 2 || rules[0].ID != standardID || rules[0].Priority != 8 || rules[1].RawPF != raw || rules[1].Priority != 9 {
		t.Fatalf("mixed visible order changed hidden priority or raw text: %+v err=%v", rules, err)
	}
	if err := svc.ReorderFirewallTrafficRules([]networkServiceInterfaces.FirewallReorderRequest{
		{ID: hidden.ID, Priority: 1}, {ID: standardID, Priority: 2}, {ID: advancedID, Priority: 3},
	}); !errors.Is(err, ErrHiddenFirewallRuleMutation) {
		t.Fatalf("hidden rule reorder allowed: %v", err)
	}
	if err := svc.DeleteFirewallTrafficRules([]uint{hidden.ID, advancedID}); !errors.Is(err, ErrHiddenFirewallRuleMutation) {
		t.Fatalf("hidden mixed deletion allowed: %v", err)
	}
	if err := svc.EditFirewallTrafficRule(hidden.ID, &networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{
		Name: "replacement", Kind: "advanced", RawPF: "pass all",
	}); !errors.Is(err, ErrHiddenFirewallRuleMutation) {
		t.Fatalf("hidden row replaced by raw text: %v", err)
	}
	if err := svc.DeleteFirewallTrafficRules([]uint{standardID, advancedID}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&hidden, hidden.ID).Error; err != nil || hidden.Priority != 7 || hidden.Visible {
		t.Fatalf("hidden row lost on mixed deletion: %+v err=%v", hidden, err)
	}
}

func TestFirewallValidationErrorMapsToAdvancedRowAndSnippetLine(t *testing.T) {
	raw := "# friends exception\npass in from <friends> to any\nblock in invalid-token\n"
	rules := []networkModels.FirewallTrafficRule{
		{ID: 10, Priority: 1, Enabled: true, Action: "pass", Direction: "in", Family: "inet", Protocol: "tcp"},
		{ID: 11, Name: "raw exception", Priority: 2, Enabled: true, Kind: "advanced", RawPF: raw},
		{ID: 12, Priority: 3, Enabled: true, Action: "block", Direction: "in", Family: "inet", Protocol: "tcp"},
	}
	text, err := (&Service{}).renderTrafficRules(rules, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for index, line := range strings.Split(text, "\n") {
		if line == "block in invalid-token" {
			found = true
			err := formatPFValidationError(fmt.Errorf("output: /candidate/traffic-rules.conf:%d: syntax error", index+1), map[string]string{"traffic-rules.conf": text})
			if !strings.Contains(err.Error(), `advanced PF id=11 name="raw exception" snippet-line=3`) {
				t.Fatalf("raw error lacks row/line: %v", err)
			}
		}
		if strings.Contains(line, `label "sylve_trf_12"`) && firewallSnippetLocation(text, index+1) != "" {
			t.Fatalf("following standard row was attributed to a raw snippet")
		}
	}
	if !found {
		t.Fatal("offending raw PF line was not rendered")
	}
}

func stubFirewallDynamicInterfaces(t *testing.T) {
	t.Helper()
	previous := firewallGetInterface
	firewallGetInterface = func(name string) (*iface.Interface, error) {
		if name == "igb1.3" || name == "wg0" {
			return &iface.Interface{Name: name}, nil
		}
		return nil, fmt.Errorf("interface not found")
	}
	t.Cleanup(func() { firewallGetInterface = previous })
}

func TestFirewallDynamicDNSRedirectAndRDRPass(t *testing.T) {
	stubFirewallDynamicInterfaces(t)
	r := networkServiceInterfaces.UpsertFirewallNATRuleRequest{Name: "DNS", NATType: "dnat", Family: "inet", Protocol: "tcp_udp",
		IngressInterfaces: []string{"igb1.3"}, DestRaw: "(igb1.3)", DNATTargetRaw: "(igb1.3)", DstPortsRaw: "53", RedirectPortsRaw: "153"}
	if err := (&Service{}).validateFirewallNATRuleRequest(&r); err != nil {
		t.Fatal(err)
	}
	rule := networkModels.FirewallNATRule{ID: 5, Name: "DNS", Enabled: true, NATType: "dnat", Family: "inet", Protocol: "tcp_udp",
		IngressInterfaces: r.IngressInterfaces, DestRaw: r.DestRaw, DNATTargetRaw: r.DNATTargetRaw, DstPortsRaw: "53", RedirectPortsRaw: "153"}
	for _, pass := range []bool{false, true} {
		rule.PassRedirectedTraffic = pass
		text, err := (&Service{}).renderNATRules([]networkModels.FirewallNATRule{rule}, nil)
		keyword := "rdr"
		if pass {
			keyword += " pass"
		}
		if err != nil || !strings.Contains(text, keyword+" on igb1.3 inet proto { tcp, udp } from any to (igb1.3) port 53 tag sylve_nat_5 -> (igb1.3) port 153") {
			t.Fatalf("pass=%t: text=%s err=%v", pass, text, err)
		}
	}
	for _, value := range []string{"(missing0)", "(igb1.3)\npass all", "(igb1.3:network)", "($lan_if)"} {
		r.DNATTargetRaw = value
		if err := (&Service{}).validateFirewallNATRuleRequest(&r); err == nil {
			t.Errorf("invalid dynamic expression accepted: %q", value)
		}
	}
}

func TestFirewallBINATDynamicAddressesExcludeInterfaceAliases(t *testing.T) {
	stubFirewallDynamicInterfaces(t)
	rule := networkModels.FirewallNATRule{ID: 6, Enabled: true, NATType: "binat", Family: "inet",
		EgressInterfaces: []string{"wg0"}, SourceRaw: "(igb1.3)", TranslateMode: "address", TranslateToRaw: "(wg0)"}
	text, err := (&Service{}).renderNATRules([]networkModels.FirewallNATRule{rule}, nil)
	if err != nil || !strings.Contains(text, "from (igb1.3:0) to any tag sylve_nat_6 -> (wg0:0)") {
		t.Fatalf("BINAT dynamic addresses permit multiple aliases: text=%s err=%v", text, err)
	}
	rule.NATType = "snat"
	text, err = (&Service{}).renderNATRules([]networkModels.FirewallNATRule{rule}, nil)
	if err != nil || !strings.Contains(text, "from (igb1.3) to any tag sylve_nat_6 -> (wg0)") {
		t.Fatalf("BINAT alias restriction leaked into SNAT: text=%s err=%v", text, err)
	}
}

func TestFirewallNATLiteralAndHostTargetSelection(t *testing.T) {
	for _, test := range []struct {
		name, raw, family, want string
		hosts                   []string
	}{
		{name: "IPv4 literal", raw: "192.0.2.10", family: "inet", want: "192.0.2.10"},
		{name: "IPv6 literal", raw: "2001:db8::10", family: "inet6", want: "2001:db8::10"},
		{name: "literal family mismatch", raw: "192.0.2.10", family: "inet6"},
		{name: "literal CIDR", raw: "192.0.2.10/32", family: "inet"},
		{name: "dynamic interface", raw: " (igb1.3) ", family: "inet", want: "(igb1.3)"},
		{name: "missing target", family: "inet"},
		{name: "empty Host", hosts: []string{}, family: "inet"},
		{name: "single Host", hosts: []string{"192.0.2.10"}, family: "inet", want: "192.0.2.10"},
		{name: "duplicate Host", hosts: []string{"192.0.2.10", "192.0.2.10"}, family: "inet", want: "192.0.2.10"},
		{name: "mixed Host families", hosts: []string{"192.0.2.10", "2001:db8::10"}, family: "inet", want: "192.0.2.10"},
		{name: "multiple Hosts", hosts: []string{"192.0.2.10", "192.0.2.11"}, family: "inet"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var object *networkModels.Object
			if test.hosts != nil {
				object = &networkModels.Object{ID: 42, Type: "Host"}
				for _, value := range test.hosts {
					object.Entries = append(object.Entries, networkModels.ObjectEntry{Value: value})
				}
			}
			selector, err := firewallNATTargetSelector(test.raw, object, test.family, "all", "single")
			if selector != test.want || (err != nil) != (test.want == "") {
				t.Fatalf("selector=%q err=%v; want=%q", selector, err, test.want)
			}
		})
	}
}

func TestFirewallFQDNScopesAndTargetHandling(t *testing.T) {
	obj := &networkModels.Object{ID: 9, Type: "FQDN", Resolutions: buildResolutionRows(9,
		[]string{"10.0.0.10", "8.8.8.8", "1.1.1.1", "8.8.8.8", "100.64.0.1", "192.0.2.1", "fd00::10", "2606:4700::1111"})}
	if values := firewallNATTargetValues(obj, "inet", "private"); !reflect.DeepEqual(values, []string{"10.0.0.10"}) {
		t.Fatalf("private values=%v", values)
	}
	if values := firewallNATTargetValues(obj, "inet", "public"); !reflect.DeepEqual(values, []string{"1.1.1.1", "8.8.8.8"}) {
		t.Fatalf("public values=%v", values)
	}
	if _, err := firewallNATTargetSelector("", obj, "inet", "public", "single"); err == nil {
		t.Fatal("multiple eligible answers must not silently select one")
	}
	rule := networkModels.FirewallNATRule{ID: 10, Name: "FQDN", Enabled: true, NATType: "dnat", IngressInterfaces: []string{"em0"},
		Family: "inet", Protocol: "tcp", DNATTargetObj: obj, TargetAddressScope: "public", TargetHandling: "round_robin", RedirectPortsRaw: "8080"}
	text, err := (&Service{}).renderNATRules([]networkModels.FirewallNATRule{rule}, nil)
	if err != nil || !strings.Contains(text, "-> { 1.1.1.1, 8.8.8.8 } port 8080 round-robin") {
		t.Fatalf("pool text=%s err=%v", text, err)
	}
	rule.TargetAddressScope, rule.TargetHandling = "private", "single"
	text, err = (&Service{}).renderNATRules([]networkModels.FirewallNATRule{rule}, nil)
	if err != nil || !strings.Contains(text, "-> 10.0.0.10 port 8080") || strings.Contains(text, "round-robin") {
		t.Fatalf("single text=%s err=%v", text, err)
	}
	rule.TargetHandling = "round_robin"
	text, err = (&Service{}).renderNATRules([]networkModels.FirewallNATRule{rule}, nil)
	if err != nil || !strings.Contains(text, "-> 10.0.0.10 port 8080 round-robin") {
		t.Fatalf("explicit one-address pool lost its mode: text=%s err=%v", text, err)
	}
	rule.Family = "inet6"
	rule.TargetAddressScope = "public"
	text, err = (&Service{}).renderNATRules([]networkModels.FirewallNATRule{rule}, nil)
	if err != nil || !strings.Contains(text, "-> 2606:4700::1111") {
		t.Fatalf("IPv6 pool text=%s err=%v", text, err)
	}
	obj.Resolutions = nil
	if text, err := (&Service{}).renderNATRules([]networkModels.FirewallNATRule{rule}, nil); err == nil || text != "" {
		t.Fatalf("unresolved target must reject candidate: text=%s err=%v", text, err)
	}
}

func TestFirewallPublicAddressRejectsSpecialPurposeSpace(t *testing.T) {
	for _, value := range []string{"10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.1.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "::1", "fe80::1", "fd00::1", "ff02::1", "2001:db8::1", "3fff::1", "5000::1"} {
		if firewallPublicAddress(netip.MustParseAddr(value)) {
			t.Errorf("non-public address classified public: %s", value)
		}
	}
	for _, value := range []string{"1.1.1.1", "8.8.8.8", "192.0.0.9", "2606:4700::1111", "2001:1::1", "2001:3::1"} {
		if !firewallPublicAddress(netip.MustParseAddr(value)) {
			t.Errorf("public address rejected: %s", value)
		}
	}
}

func TestFirewallFQDNTargetValidationUsesApplicableFamilies(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.Object{}, &networkModels.ObjectEntry{}, &networkModels.ObjectResolution{})
	obj := networkModels.Object{Type: "FQDN", Name: "target", Entries: []networkModels.ObjectEntry{{Value: "target.example"}}}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(buildResolutionRows(obj.ID, []string{"10.0.0.10", "fd00::10", "fd00::11"})).Error; err != nil {
		t.Fatal(err)
	}
	req := validFirewallNATRuleRequest("fqdn-target")
	req.Family, req.SourceRaw, req.TranslateMode, req.TranslateToObjID = "any", "10.0.0.0/24", "address", &obj.ID
	if err := svc.validateFirewallNATRuleRequest(&req); err != nil {
		t.Fatalf("v6 cardinality must not invalidate a v4-only match: %v", err)
	}
	req.SourceRaw = "any"
	if err := svc.validateFirewallNATRuleRequest(&req); !errors.Is(err, ErrInvalidFirewallNATRule) {
		t.Fatalf("both families apply; two v6 answers must fail single-address mode: %v", err)
	}
	req.TargetHandling = "round_robin"
	if err := svc.validateFirewallNATRuleRequest(&req); err != nil {
		t.Fatalf("explicit dual-family pool must work: %v", err)
	}
	req.NATType = "binat"
	if err := svc.validateFirewallNATRuleRequest(&req); !errors.Is(err, ErrInvalidFirewallNATRule) {
		t.Fatalf("BINAT must reject pool mode: %v", err)
	}
}

func TestFirewallFQDNTargetHydrationDoesNotFilterSharedMatchingObject(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.Object{}, &networkModels.ObjectEntry{}, &networkModels.ObjectResolution{})
	obj := networkModels.Object{Type: "FQDN", Name: "mixed", Entries: []networkModels.ObjectEntry{{Value: "mixed.example"}}}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(buildResolutionRows(obj.ID, []string{"10.0.0.10", "8.8.8.8"})).Error; err != nil {
		t.Fatal(err)
	}
	traffic := []networkModels.FirewallTrafficRule{{Enabled: true, Action: "pass", Direction: "in", Protocol: "any", Family: "inet",
		SourceObj: &networkModels.Object{ID: obj.ID, Type: "FQDN"}}}
	nat := []networkModels.FirewallNATRule{{Enabled: true, NATType: "dnat", IngressInterfaces: []string{"em0"}, Family: "inet", Protocol: "tcp",
		DNATTargetObj: &networkModels.Object{ID: obj.ID, Type: "FQDN"}, TargetAddressScope: "private", TargetHandling: "single"}}
	tables, err := svc.buildFirewallObjectTables(traffic, nat)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tables[obj.ID].InetValues, []string{"10.0.0.10", "8.8.8.8"}) {
		t.Fatalf("target scope changed shared matching table: %+v", tables)
	}
	text, err := svc.renderNATRules(nat, tables)
	if err != nil || !strings.Contains(text, "-> 10.0.0.10") {
		t.Fatalf("target instance did not receive cached resolutions: text=%s err=%v", text, err)
	}
	if tables := buildFirewallObjectTables(nil, nat); len(tables) != 0 {
		t.Fatalf("literal translation targets must not create unused PF tables: %+v", tables)
	}
}

func TestFirewallFQDNTargetDependenciesAndNATSnapshotRestore(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.Object{}, &networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{}, &networkModels.FirewallNATRule{})
	if err := db.Create(&models.BasicSettings{}).Error; err != nil {
		t.Fatal(err)
	}
	obj := networkModels.Object{Name: "target", Type: "FQDN", Entries: []networkModels.ObjectEntry{{Value: "target.example"}}}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(buildResolutionRows(obj.ID, []string{"10.0.0.10", "8.8.8.8"})).Error; err != nil {
		t.Fatal(err)
	}
	req := networkServiceInterfaces.UpsertFirewallNATRuleRequest{Name: "target", NATType: "dnat", Family: "inet", Protocol: "tcp",
		IngressInterfaces: []string{"igb1.3"}, DNATTargetObjID: &obj.ID, PassRedirectedTraffic: boolPtr(true),
		TargetAddressScope: "private", TargetHandling: "round_robin", Enabled: boolPtr(false)}
	id, err := svc.CreateFirewallNATRule(&req)
	if err != nil {
		t.Fatal(err)
	}
	objects := []networkModels.Object{obj}
	if err := svc.populateObjectUsage(objects); err != nil || !objects[0].IsUsed || objects[0].IsUsedBy != "firewall" {
		t.Fatalf("FQDN translation dependency missing: objects=%+v err=%v", objects, err)
	}
	if err := svc.DeleteObject(obj.ID); !errors.Is(err, ErrNetworkObjectConflict) {
		t.Fatalf("FQDN target object deleted despite NAT reference: %v", err)
	}
	snapshot, err := snapshotFirewallNATRules(db)
	if err != nil {
		t.Fatal(err)
	}
	req.PassRedirectedTraffic, req.TargetAddressScope, req.TargetHandling = boolPtr(false), "public", "single"
	if err := svc.EditFirewallNATRule(id, &req); err != nil {
		t.Fatal(err)
	}
	if err := restoreFirewallNATRules(db, snapshot); err != nil {
		t.Fatal(err)
	}
	var restored networkModels.FirewallNATRule
	if err := db.First(&restored, id).Error; err != nil || restored.Enabled || !restored.PassRedirectedTraffic ||
		restored.TargetAddressScope != "private" || restored.TargetHandling != "round_robin" || restored.DNATTargetObjID == nil || *restored.DNATTargetObjID != obj.ID {
		t.Fatalf("NAT target options lost on snapshot restoration: %+v err=%v", restored, err)
	}
}

func TestFirewallNATFamiliesIntersectMatchingAndTargetAddresses(t *testing.T) {
	obj := &networkModels.Object{ID: 1, Type: "FQDN", Resolutions: buildResolutionRows(1, []string{"10.0.0.1", "fd00::1"})}
	rule := networkModels.FirewallNATRule{ID: 7, Enabled: true, NATType: "dnat", Protocol: "tcp", Family: "any",
		IngressInterfaces: []string{"wg1"}, SourceObj: obj, DNATTargetRaw: "10.0.0.10"}
	tables := buildFirewallObjectTables(nil, []networkModels.FirewallNATRule{rule})
	text, err := (&Service{}).renderNATRules([]networkModels.FirewallNATRule{rule}, tables)
	if err != nil || strings.Contains(text, "inet6") || !strings.Contains(text, "rdr on wg1 inet proto tcp") {
		t.Fatalf("IPv4 target failed due to unrelated IPv6 matching addresses: text=%s err=%v", text, err)
	}
	rule.SourceRaw, rule.SourceObj = "fd00::/64", nil
	if text, err := (&Service{}).renderNATRules([]networkModels.FirewallNATRule{rule}, nil); err == nil || text != "" {
		t.Fatalf("enabled family mismatch silently omitted instead of rejecting candidate: text=%s err=%v", text, err)
	}
}

func TestFirewallMissingNATReferencesRejectCandidate(t *testing.T) {
	missing := uint(404)
	for _, ref := range []string{"source", "destination", "target", "port"} {
		rule := networkModels.FirewallNATRule{ID: 7, Enabled: true, NATType: "dnat", Protocol: "tcp", Family: "inet",
			IngressInterfaces: []string{"wg1"}, DNATTargetRaw: "10.0.0.10"}
		switch ref {
		case "source":
			rule.SourceObjID = &missing
		case "destination":
			rule.DestObjID = &missing
		case "target":
			rule.DNATTargetRaw, rule.DNATTargetObjID = "", &missing
		case "port":
			rule.DstPortObjID = &missing
		}
		if text, err := (&Service{}).renderNATRules([]networkModels.FirewallNATRule{rule}, nil); err == nil || text != "" {
			t.Fatalf("missing %s reference silently dropped or broadened enabled NAT: text=%s err=%v", ref, text, err)
		}
	}
}

func TestFirewallEdge1KnownNATFragmentsPreserveMaskAndPriority(t *testing.T) {
	stubFirewallDynamicInterfaces(t)
	target := &networkModels.Object{ID: 1, Name: "rds.zbr", Type: "FQDN", Resolutions: buildResolutionRows(1, []string{"10.32.50.10"})}
	rules := []networkModels.FirewallNATRule{
		{ID: 3, Name: "catch-all", Priority: 30, Enabled: true, NATType: "snat", Family: "inet", Protocol: "any",
			EgressInterfaces: []string{"igb0"}, SourceRaw: "10.32.50.0/24", TranslateMode: "address", TranslateToRaw: "107.155.64.194"},
		{ID: 2, Name: "specific WG public NAT", Priority: 20, Enabled: true, NATType: "snat", Family: "inet", Protocol: "any",
			EgressInterfaces: []string{"wg0"}, SourceRaw: "23.227.162.144/28", TranslateMode: "address", TranslateToRaw: "(wg0)"},
		{ID: 1, Name: "WG RDS", Priority: 10, Enabled: true, NATType: "dnat", Family: "inet", Protocol: "tcp", PassRedirectedTraffic: true,
			IngressInterfaces: []string{"wg1"}, DestRaw: "10.4.21.1/24", DstPortsRaw: "3389", DNATTargetObj: target},
		{ID: 4, Name: "reflection SNAT", Priority: 15, Enabled: true, NATType: "snat", Family: "inet", Protocol: "any",
			EgressInterfaces: []string{"igb1.3"}, SourceRaw: "10.32.50.0/24", DestRaw: "10.32.50.0/24", TranslateMode: "address", TranslateToRaw: "10.32.50.251"},
	}
	text, err := (&Service{}).renderNATRules(rules, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"rdr pass on wg1 inet proto tcp from any to 10.4.21.1/24 port 3389 tag sylve_nat_1 -> 10.32.50.10",
		"nat on igb1.3 inet from 10.32.50.0/24 to 10.32.50.0/24 tag sylve_nat_4 -> 10.32.50.251",
		"nat on wg0 inet from 23.227.162.144/28 to any tag sylve_nat_2 -> (wg0)",
	} {
		if !strings.Contains(text, fragment) {
			t.Errorf("edge1 policy fragment changed: %s\n%s", fragment, text)
		}
	}
	if strings.Index(text, "tag sylve_nat_2") > strings.Index(text, "tag sylve_nat_3") {
		t.Fatalf("specific NAT was moved after catch-all:\n%s", text)
	}
}

func TestFirewallFQDNRefreshRejectsUnusableTargetsAndRetainsLastGoodAnswers(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.FirewallNATRule{}, &networkModels.Object{},
		&networkModels.ObjectEntry{}, &networkModels.ObjectResolution{})
	if err := db.Create(&models.BasicSettings{}).Error; err != nil {
		t.Fatal(err)
	}
	obj := networkModels.Object{Type: "FQDN", Name: "service", Entries: []networkModels.ObjectEntry{{Value: "service.example"}}}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(buildResolutionRows(obj.ID, []string{"10.0.0.10"})).Error; err != nil {
		t.Fatal(err)
	}
	rule := networkModels.FirewallNATRule{Name: "forward", Enabled: true, NATType: "dnat", IngressInterfaces: []string{"em0"},
		Family: "inet", Protocol: "tcp", DNATTargetObjID: &obj.ID, TargetAddressScope: "private", TargetHandling: "single"}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	previousResolver := refreshNetworkObjectFQDN
	t.Cleanup(func() { refreshNetworkObjectFQDN = previousResolver })
	answers := []string{"8.8.8.8"}
	refreshNetworkObjectFQDN = func(context.Context, string) ([]string, error) { return answers, nil }
	for _, candidate := range [][]string{{"8.8.8.8"}, {"10.0.0.11", "10.0.0.12"}, {}} {
		answers = candidate
		if err := svc.RefreshObjectByID(obj.ID); err == nil {
			t.Fatalf("unusable single/private refresh accepted: %v", answers)
		}
		var values []string
		if err := db.Model(&networkModels.ObjectResolution{}).Where("object_id = ?", obj.ID).Pluck("resolved_value", &values).Error; err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(values, []string{"10.0.0.10"}) {
			t.Fatalf("last-good answers lost: %v", values)
		}
		var refreshed networkModels.Object
		if err := db.First(&refreshed, obj.ID).Error; err != nil || refreshed.LastRefreshError == "" {
			t.Fatalf("refresh failure not reported: %+v err=%v", refreshed, err)
		}
	}
	answers = []string{"10.0.0.11"}
	if err := svc.RefreshObjectByID(obj.ID); err != nil {
		t.Fatalf("usable answer did not recover: %v", err)
	}
	var values []string
	if err := db.Model(&networkModels.ObjectResolution{}).Where("object_id = ?", obj.ID).Pluck("resolved_value", &values).Error; err != nil || !reflect.DeepEqual(values, answers) {
		t.Fatalf("valid answer was not stored: values=%v err=%v", values, err)
	}
}

func TestFirewallBINATSourceRefreshRetainsLastGoodAnswer(t *testing.T) {
	for _, kind := range []string{"FQDN", "List"} {
		t.Run(kind, func(t *testing.T) {
			svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.FirewallNATRule{}, &networkModels.Object{},
				&networkModels.ObjectEntry{}, &networkModels.ObjectResolution{}, &networkModels.ObjectListSnapshot{})
			if err := db.Create(&models.BasicSettings{}).Error; err != nil {
				t.Fatal(err)
			}
			answers := []string{"10.0.0.11", "10.0.0.12"}
			entry := "source.example"
			if kind == "List" {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprint(w, strings.Join(answers, "\n"))
				}))
				defer server.Close()
				entry = server.URL
			}
			obj := networkModels.Object{Type: kind, Name: "source", Entries: []networkModels.ObjectEntry{{Value: entry}}}
			if err := db.Create(&obj).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(buildResolutionRows(obj.ID, []string{"10.0.0.10"})).Error; err != nil {
				t.Fatal(err)
			}
			if kind == "List" {
				if err := storeListSnapshot(db, obj.ID, objectValuesChecksum([]string{"10.0.0.10"}), []string{"10.0.0.10"}); err != nil {
					t.Fatal(err)
				}
			}
			rule := networkModels.FirewallNATRule{Name: "one-to-one", Enabled: true, NATType: "binat", EgressInterfaces: []string{"em0"},
				Family: "inet", SourceObjID: &obj.ID, TranslateMode: "address", TranslateToRaw: "192.0.2.10"}
			if err := db.Create(&rule).Error; err != nil {
				t.Fatal(err)
			}
			previousResolver := refreshNetworkObjectFQDN
			t.Cleanup(func() { refreshNetworkObjectFQDN = previousResolver })
			refreshNetworkObjectFQDN = func(context.Context, string) ([]string, error) { return answers, nil }
			for _, candidate := range [][]string{answers, {}} {
				answers = candidate
				if err := svc.RefreshObjectByID(obj.ID); err == nil {
					t.Fatalf("unusable BINAT source refresh accepted: %v", answers)
				}
				var values []string
				var err error
				if kind == "List" {
					values, err = svc.loadListSnapshotValues(obj.ID)
				} else {
					err = db.Model(&networkModels.ObjectResolution{}).Where("object_id = ?", obj.ID).Pluck("resolved_value", &values).Error
				}
				if err != nil || !reflect.DeepEqual(values, []string{"10.0.0.10"}) {
					t.Fatalf("BINAT source last-good answer lost: values=%v err=%v", values, err)
				}
				var refreshed networkModels.Object
				if err := db.First(&refreshed, obj.ID).Error; err != nil || refreshed.LastRefreshError == "" {
					t.Fatalf("BINAT source refresh failure not reported: %+v err=%v", refreshed, err)
				}
			}
			answers = []string{"10.0.0.11"}
			if err := svc.RefreshObjectByID(obj.ID); err != nil {
				t.Fatalf("valid BINAT source refresh failed: %v", err)
			}
			if err := db.Model(&rule).Update("nat_type", "snat").Error; err != nil {
				t.Fatal(err)
			}
			answers = []string{"10.0.0.12", "10.0.0.13"}
			if err := svc.RefreshObjectByID(obj.ID); err != nil {
				t.Fatalf("single-source restriction incorrectly applied to SNAT: %v", err)
			}
		})
	}
}

func TestFirewallDynamicAddressReferencesProtectStandardSwitches(t *testing.T) {
	stubFirewallDynamicInterfaces(t)
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallTrafficRule{}, &networkModels.FirewallNATRule{})
	rule := networkModels.FirewallTrafficRule{Name: "dynamic", SourceRaw: "(igb1.3)"}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.checkStandardSwitchExternalUsage("igb1.3"); !errors.Is(err, ErrStandardSwitchInUse) {
		t.Fatalf("dynamic traffic reference was not protected: %v", err)
	}
	if err := db.Delete(&rule).Error; err != nil {
		t.Fatal(err)
	}
	nat := networkModels.FirewallNATRule{Name: "dynamic-nat", TranslateToRaw: "(igb1.3)"}
	if err := db.Create(&nat).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.checkStandardSwitchExternalUsage("igb1.3"); !errors.Is(err, ErrStandardSwitchInUse) {
		t.Fatalf("dynamic NAT reference was not protected: %v", err)
	}
}

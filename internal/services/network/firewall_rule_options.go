// SPDX-License-Identifier: BSD-2-Clause

package network

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/network/interfaceref"
	"github.com/alchemillahq/sylve/pkg/network/iface"
)

var firewallGetInterface = iface.Get

var firewallICMPTypes = []string{
	"echoreq", "echorep", "unreach", "squench", "redir", "althost", "routeradv", "routersol",
	"timex", "paramprob", "timereq", "timerep", "inforeq", "inforep", "maskreq", "maskrep",
	"trace", "dataconv", "mobredir", "ipv6-where", "ipv6-here", "mobregreq", "mobregrep", "skip", "photuris",
}

var firewallICMP6Types = []string{
	"unreach", "toobig", "timex", "paramprob", "echoreq", "echorep", "groupqry", "listqry",
	"grouprep", "listenrep", "groupterm", "listendone", "routersol", "routeradv", "neighbrsol",
	"neighbradv", "redir", "routrrenum", "wrureq", "wrurep", "fqdnreq", "fqdnrep", "niqry",
	"nirep", "mtraceresp", "mtrace", "listenrepv2",
}

func normalizeFirewallOption(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	return value
}

func firewallProtocolFamily(family, protocol string) string {
	switch normalizeProtocol(protocol) {
	case "icmp":
		return "inet"
	case "icmp6":
		return "inet6"
	default:
		return normalizeFamily(family)
	}
}

func validateFirewallTrafficOptions(req *networkServiceInterfaces.UpsertFirewallTrafficRuleRequest) error {
	state := normalizeFirewallOption(req.StatePolicy, "default")
	response := normalizeFirewallOption(req.BlockResponse, "default")
	if !slices.Contains([]string{"default", "keep", "none"}, state) {
		return fmt.Errorf("invalid_state_policy")
	}
	if !slices.Contains([]string{"default", "drop", "return"}, response) {
		return fmt.Errorf("invalid_block_response")
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action != "pass" && state != "default" {
		return fmt.Errorf("state_policy_requires_pass_action")
	}
	if action != "block" && response != "default" {
		return fmt.Errorf("block_response_requires_block_action")
	}
	protocol := normalizeProtocol(req.Protocol)
	family := normalizeFamily(req.Family)
	if (protocol == "icmp" && family == "inet6") || (protocol == "icmp6" && family == "inet") {
		return fmt.Errorf("icmp_protocol_family_mismatch")
	}
	if len(req.ICMPTypes) > 256 {
		return fmt.Errorf("too_many_icmp_types")
	}
	allowed := firewallICMPTypes
	if protocol == "icmp6" {
		allowed = firewallICMP6Types
	}
	seen := map[string]bool{}
	for _, value := range req.ICMPTypes {
		if protocol != "icmp" && protocol != "icmp6" {
			return fmt.Errorf("icmp_types_require_icmp_protocol")
		}
		if !slices.Contains(allowed, value) || seen[value] {
			return fmt.Errorf("invalid_or_duplicate_icmp_type: %q", value)
		}
		seen[value] = true
	}
	return nil
}

func validateFirewallRawPF(raw string) error {
	if len(raw) > MaxFirewallAdvancedSectionBytes || strings.ContainsAny(raw, "\x00\r") {
		return fmt.Errorf("invalid_advanced_pf_text")
	}
	statements := 0
	continuing := false
	braces := 0
	for index, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !continuing {
			keyword := strings.Fields(line)[0]
			if !slices.Contains([]string{"pass", "block", "match", "antispoof"}, keyword) {
				return fmt.Errorf("advanced_pf_filtering_only: line=%d", index+1)
			}
			statements++
		}
		quoted, escaped := false, false
		var options strings.Builder
		for i, r := range line {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == '"' {
				quoted = !quoted
				options.WriteByte(' ')
			}
			if !quoted && r == '#' {
				line = strings.TrimSpace(line[:i])
				break
			}
			if !quoted && r == ';' {
				return fmt.Errorf("advanced_pf_invalid_statement_separator: line=%d", index+1)
			}
			if !quoted {
				options.WriteRune(r)
				if r == '{' {
					braces++
				} else if r == '}' {
					braces--
					if braces < 0 {
						return fmt.Errorf("advanced_pf_unbalanced_braces: line=%d", index+1)
					}
				}
			}
		}
		if quoted {
			return fmt.Errorf("advanced_pf_unterminated_quote: line=%d", index+1)
		}
		for _, token := range strings.FieldsFunc(options.String(), func(r rune) bool {
			return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_')
		}) {
			if slices.Contains([]string{"nat-to", "rdr-to", "binat-to"}, token) {
				return fmt.Errorf("advanced_pf_filtering_only: line=%d", index+1)
			}
		}
		continuing = strings.HasSuffix(line, "\\")
	}
	if statements == 0 || continuing || braces != 0 {
		return fmt.Errorf("advanced_pf_requires_complete_filter_rules")
	}
	return nil
}

func applyFirewallTrafficOptions(rule *networkModels.FirewallTrafficRule, req *networkServiceInterfaces.UpsertFirewallTrafficRuleRequest) {
	rule.Kind = normalizeFirewallOption(req.Kind, "standard")
	rule.RawPF = req.RawPF
	rule.StatePolicy = normalizeFirewallOption(req.StatePolicy, "default")
	rule.BlockResponse = normalizeFirewallOption(req.BlockResponse, "default")
	rule.ICMPTypes = append([]string{}, req.ICMPTypes...)
	if rule.Kind == "advanced" {
		rule.Log, rule.Quick = false, false
	}
}

func (s *Service) validateFirewallAddress(value, family string, allowCIDR bool, field string, allowAny bool) error {
	if name, ok := interfaceref.DynamicAddressInterface(value); ok {
		if _, err := firewallGetInterface(name); err != nil {
			return fmt.Errorf("%s_interface_unavailable: %s: %w", field, name, err)
		}
		if s != nil && s.DB != nil {
			return s.rejectFilteredStandardBridgeInterfaces(name)
		}
		return nil
	}
	return validateFamilyAgainstRawAddress(value, family, allowCIDR, field, allowAny)
}

var firewallNonPublicIPv4 = firewallPrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16",
	"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
)

var firewallNonPublicIPv6 = firewallPrefixes("2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20")

var firewallPublicIPv6Exceptions = firewallPrefixes(
	"2001:1::1/128", "2001:1::2/128", "2001:1::3/128", "2001:3::/32", "2001:4:112::/48",
	"2001:20::/28", "2001:30::/28",
)

var firewallPublicIPv6Allocations = firewallPrefixes("2000::/3", "64:ff9b::/96")

func firewallPrefixes(values ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		out = append(out, netip.MustParsePrefix(value))
	}
	return out
}

func firewallPublicAddress(address netip.Addr) bool {
	if !address.IsGlobalUnicast() || address.IsPrivate() {
		return false
	}
	if address.Is4() {
		if address.String() == "192.0.0.9" || address.String() == "192.0.0.10" {
			return true
		}
		for _, prefix := range firewallNonPublicIPv4 {
			if prefix.Contains(address) {
				return false
			}
		}
		return true
	}
	allocated := false
	for _, prefix := range firewallPublicIPv6Allocations {
		allocated = allocated || prefix.Contains(address)
	}
	if !allocated {
		return false
	}
	for _, prefix := range firewallPublicIPv6Exceptions {
		if prefix.Contains(address) {
			return true
		}
	}
	for _, prefix := range firewallNonPublicIPv6 {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func firewallNATTargetValues(obj *networkModels.Object, family, scope string) []string {
	values := []string{}
	for _, value := range objectValuesForFirewall(obj) {
		address, err := netip.ParseAddr(value)
		if err != nil || address.Zone() != "" {
			continue
		}
		address = address.Unmap()
		if address.IsUnspecified() || address.IsMulticast() || (family == "inet" && !address.Is4()) || (family == "inet6" && !address.Is6()) {
			continue
		}
		if (scope == "private" && !address.IsPrivate()) || (scope == "public" && !firewallPublicAddress(address)) {
			continue
		}
		values = append(values, address.String())
	}
	return uniqueStrings(values)
}

func firewallNATTargetSelector(raw string, obj *networkModels.Object, family, scope, handling string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" || (obj != nil && obj.Type == "Host") {
		if scope != "all" || handling != "single" {
			return "", fmt.Errorf("target_scope_and_pool_require_fqdn_object")
		}
		if raw != "" {
			if _, dynamic := interfaceref.DynamicAddressInterface(raw); !dynamic {
				if err := validateFamilyAgainstRawAddress(raw, family, false, "nat_target", false); err != nil {
					return "", err
				}
			}
			return raw, nil
		}
		hosts := []string{}
		for _, value := range objectValuesForFirewall(obj) {
			if validateFamilyAgainstRawAddress(value, family, false, "nat_target", false) == nil {
				hosts = append(hosts, value)
			}
		}
		hosts = uniqueStrings(hosts)
		if len(hosts) != 1 {
			return "", fmt.Errorf("invalid_nat_target: object_id=%d requires one host address, got %d", obj.ID, len(hosts))
		}
		return hosts[0], nil
	}
	if obj == nil || obj.Type != "FQDN" {
		return "", fmt.Errorf("nat_target_requires_host_or_fqdn_object")
	}
	values := firewallNATTargetValues(obj, family, scope)
	if len(values) == 0 {
		return "", fmt.Errorf("fqdn_target_has_no_eligible_addresses: object=%d family=%s scope=%s", obj.ID, family, scope)
	}
	if handling == "single" && len(values) != 1 {
		return "", fmt.Errorf("fqdn_target_requires_single_address: object=%d family=%s addresses=%d", obj.ID, family, len(values))
	}
	return compactPFList(values), nil
}

func firewallBINATSourceSelector(obj *networkModels.Object, family string, tables map[uint]firewallObjectTable) (string, error) {
	table := tables[obj.ID]
	values := table.InetValues
	if family == "inet6" {
		values = table.Inet6Values
	}
	if len(values) != 1 {
		return "", fmt.Errorf("binat_source_requires_single_address: object_id=%d family=%s addresses=%d", obj.ID, family, len(values))
	}
	return values[0], nil
}

func (s *Service) validateFirewallBINATSource(req *networkServiceInterfaces.UpsertFirewallNATRuleRequest) error {
	if normalizeNATType(req.NATType) != "binat" || req.SourceObjID == nil {
		return nil
	}
	rule := networkModels.FirewallNATRule{Enabled: true, NATType: "binat", Family: req.Family, Protocol: req.Protocol,
		SourceRaw: req.SourceRaw, SourceObjID: req.SourceObjID, DestRaw: req.DestRaw, DestObjID: req.DestObjID,
		TranslateMode: req.TranslateMode, TranslateToRaw: req.TranslateToRaw, TranslateToObjID: req.TranslateToObjID,
		TargetAddressScope: req.TargetAddressScope}
	tables, err := s.buildFirewallNATRequestTables(&rule)
	if err != nil {
		return err
	}
	families := firewallNATFamilies(rule, tables)
	if len(families) == 0 {
		return fmt.Errorf("binat_source_has_no_usable_address_combination")
	}
	for _, family := range families {
		if _, err := firewallBINATSourceSelector(rule.SourceObj, family, tables); err != nil {
			return err
		}
	}
	return nil
}

func firewallRawAddressesMatchFamily(family string, values ...string) bool {
	for _, value := range values {
		if _, dynamic := interfaceref.DynamicAddressInterface(value); dynamic {
			continue
		}
		if validateFamilyAgainstRawAddress(value, family, true, "address", true) != nil {
			return false
		}
	}
	return true
}

func firewallDynamicAddressesReference(name string, values ...string) bool {
	for _, value := range values {
		if referenced, ok := interfaceref.DynamicAddressInterface(value); ok && referenced == name {
			return true
		}
	}
	return false
}

func firewallSnippetLocation(content string, lineNumber int) string {
	start := 0
	marker := ""
	for index, line := range strings.Split(content, "\n") {
		if index+1 >= lineNumber {
			break
		}
		if strings.HasPrefix(line, "# --- advanced PF ") {
			start, marker = index+1, strings.TrimSuffix(strings.TrimPrefix(line, "# --- "), " ---")
		} else if strings.HasPrefix(line, "# --- end advanced PF ") {
			marker = ""
		}
	}
	if marker == "" {
		return ""
	}
	return fmt.Sprintf(" [%s snippet-line=%d]", marker, lineNumber-start)
}

func firewallNATFamilies(rule networkModels.FirewallNATRule, tables map[uint]firewallObjectTable) []string {
	family := normalizeFamily(rule.Family)
	if rule.Protocol == "icmp" {
		if family == "inet6" {
			return nil
		}
		family = "inet"
	} else if rule.Protocol == "icmp6" {
		if family == "inet" {
			return nil
		}
		family = "inet6"
	}
	constrained := family != "any"
	for _, raw := range []string{rule.SourceRaw, rule.DestRaw} {
		_, dynamic := interfaceref.DynamicAddressInterface(raw)
		if strings.TrimSpace(raw) != "" && !strings.EqualFold(raw, "any") && !dynamic {
			constrained = true
		}
	}
	constrained = constrained || rule.SourceObj != nil || rule.DestObj != nil
	targetRaw, targetObj := rule.TranslateToRaw, rule.TranslateToObj
	if normalizeNATType(rule.NATType) == "dnat" {
		targetRaw, targetObj = rule.DNATTargetRaw, rule.DNATTargetObj
	} else if normalizeTranslateMode(rule.TranslateMode) == "interface" {
		targetRaw, targetObj = "", nil
	}
	_, dynamicTarget := interfaceref.DynamicAddressInterface(targetRaw)
	constrained = constrained || targetObj != nil || (strings.TrimSpace(targetRaw) != "" && !dynamicTarget)
	if !constrained {
		return []string{"any"}
	}
	out := []string{}
	for _, candidate := range []string{"inet", "inet6"} {
		if family != "any" && family != candidate {
			continue
		}
		if !firewallRawAddressesMatchFamily(candidate, rule.SourceRaw, rule.DestRaw, targetRaw) {
			continue
		}
		valid := true
		for _, obj := range []*networkModels.Object{rule.SourceObj, rule.DestObj} {
			if obj == nil {
				continue
			}
			table := tables[obj.ID]
			if (candidate == "inet" && table.InetName == "") || (candidate == "inet6" && table.Inet6Name == "") {
				valid = false
			}
		}
		if targetObj != nil && len(firewallNATTargetValues(targetObj, candidate, normalizeFirewallOption(rule.TargetAddressScope, "all"))) == 0 {
			valid = false
		}
		if valid {
			out = append(out, candidate)
		}
	}
	return out
}

func (s *Service) firewallNATRequestMatchFamilies(req *networkServiceInterfaces.UpsertFirewallNATRuleRequest) ([]string, error) {
	rule := networkModels.FirewallNATRule{Enabled: true, NATType: req.NATType, Family: req.Family, Protocol: req.Protocol,
		SourceRaw: req.SourceRaw, SourceObjID: req.SourceObjID, DestRaw: req.DestRaw, DestObjID: req.DestObjID}
	tables, err := s.buildFirewallNATRequestTables(&rule)
	if err != nil {
		return nil, err
	}
	return firewallNATFamilies(rule, tables), nil
}

func (s *Service) buildFirewallNATRequestTables(rule *networkModels.FirewallNATRule) (map[uint]firewallObjectTable, error) {
	for _, ref := range []struct {
		id  *uint
		obj **networkModels.Object
	}{{rule.SourceObjID, &rule.SourceObj}, {rule.DestObjID, &rule.DestObj}, {rule.TranslateToObjID, &rule.TranslateToObj}} {
		if ref.id == nil {
			continue
		}
		*ref.obj = &networkModels.Object{}
		if err := s.DB.Preload("Entries").First(*ref.obj, *ref.id).Error; err != nil {
			return nil, err
		}
	}
	return s.buildFirewallObjectTables(nil, []networkModels.FirewallNATRule{*rule})
}

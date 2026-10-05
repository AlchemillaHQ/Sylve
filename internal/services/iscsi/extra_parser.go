// SPDX-License-Identifier: BSD-2-Clause

package iscsi

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const extraConfigBanner = "# === Extra target configuration ==="

const nativeGrammarRevision = "FreeBSD releng/15.0 66b5296f1b29083634e2875ff08c32e7b6b866a8"

var nativeOptionName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

var nativeKeywords = strings.Fields("alias auth-group auth-type backend blocksize chap chap-mutual controller ctl-lun debug device-id device-type discovery-auth-group discovery-filter discovery-tcp dscp pcp foreign host-address host-nqn initiator-name initiator-portal listen listen-iser lun maxproc namespace offload option path pidfile isns-server isns-period isns-timeout port portal-group redirect serial size tag target tcp timeout transport-group af11 af12 af13 af21 af22 af23 af31 af32 af33 af41 af42 af43 be ef cs0 cs1 cs2 cs3 cs4 cs5 cs6 cs7")

type listenerReplacement struct {
	token nativeConfigToken
	value string
}

type extraConfigParser struct {
	service      *Service
	tokens       []nativeConfigToken
	index        int
	state        *targetConfiguration
	auth         map[string]bool
	names        map[string]bool
	luns         map[string]targetLUN
	groupNames   map[string]bool
	replacements []listenerReplacement
}

func nativeStringToken(token nativeConfigToken) bool {
	if token.quoted {
		return true
	}
	if token.value == "" || token.value == "{" || token.value == "}" || token.value == ";" {
		return false
	}
	return !slices.Contains(nativeKeywords, token.value)
}

func (p *extraConfigParser) peek() nativeConfigToken {
	if p.index == len(p.tokens) {
		line := 1
		if p.index != 0 {
			line = p.tokens[p.index-1].line
		}
		return nativeConfigToken{line: line}
	}
	return p.tokens[p.index]
}

func (p *extraConfigParser) take() nativeConfigToken {
	token := p.peek()
	if p.index < len(p.tokens) {
		p.index++
	}
	return token
}

func (p *extraConfigParser) expect(value string) error {
	token := p.take()
	if token.quoted || token.value != value {
		return invalidExtraConfig("invalid_extra_config_syntax", token.line)
	}
	return nil
}

func (p *extraConfigParser) argument() (nativeConfigToken, error) {
	token := p.take()
	if !nativeStringToken(token) {
		return token, invalidExtraConfig("extra_config_string_required", token.line)
	}
	return token, nil
}

func (p *extraConfigParser) arguments(count int) ([]nativeConfigToken, error) {
	tokens := make([]nativeConfigToken, count)
	for i := range tokens {
		token, err := p.argument()
		if err != nil {
			return nil, err
		}
		tokens[i] = token
	}
	return tokens, nil
}

func (p *extraConfigParser) semicolon() {
	if token := p.peek(); !token.quoted && token.value == ";" {
		p.take()
	}
}

func reservedExtraGroup(name string) bool {
	name = strings.ToLower(name)
	return strings.HasPrefix(name, "pg-") || strings.HasPrefix(name, "ag-") || name == "default"
}

func (p *extraConfigParser) authReference(token nativeConfigToken) error {
	if reservedExtraGroup(token.value) {
		return invalidExtraConfig("extra_config_reserved_group", token.line)
	}
	if !p.auth[token.value] {
		return invalidExtraConfig("extra_config_unknown_auth_group", token.line)
	}
	return nil
}

func (s *Service) parseExtraTargetConfig(ctx context.Context, text string, managed *targetConfiguration) (*targetConfiguration, string, error) {
	if len(text) > MaxExtraConfigBytes {
		return nil, "", &ExtraConfigError{ReasonCode: "extra_config_too_large", kind: ErrExtraConfigTooLarge}
	}
	return s.parseExtraTargetConfigText(ctx, text, managed)
}

func (s *Service) parseExtraTargetConfigText(ctx context.Context, text string, managed *targetConfiguration) (*targetConfiguration, string, error) {
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return nil, "", invalidExtraConfig("invalid_native_config_token", 0)
	}
	tokens, err := nativeConfigTokens(text)
	if err != nil {
		return nil, "", err
	}
	p := extraConfigParser{service: s, tokens: tokens, state: &targetConfiguration{groups: make(map[string]targetGroup)},
		auth: map[string]bool{"no-authentication": true, "no-access": true}, names: make(map[string]bool), luns: make(map[string]targetLUN), groupNames: make(map[string]bool)}
	if managed != nil {
		for _, target := range managed.targets {
			p.names[strings.ToLower(target.name)] = true
		}
		for _, lun := range managed.configuredLUNs() {
			p.luns[lun.name] = lun
		}
	}
	for p.index < len(tokens) {
		kind := p.take()
		if kind.quoted || (kind.value != "auth-group" && kind.value != "portal-group" && kind.value != "transport-group" && kind.value != "lun" && kind.value != "target" && kind.value != "controller") {
			return nil, "", invalidExtraConfig("extra_config_unsupported_stanza", kind.line)
		}
		name, err := p.argument()
		if err != nil {
			return nil, "", err
		}
		if strings.ContainsAny(name.value, "<>&") {
			return nil, "", invalidExtraConfig("extra_config_unobservable_value", name.line)
		}
		if err := p.expect("{"); err != nil {
			return nil, "", err
		}
		switch kind.value {
		case "auth-group", "portal-group", "transport-group":
			lower := strings.ToLower(name.value)
			if reservedExtraGroup(name.value) || kind.value == "auth-group" && (lower == "no-authentication" || lower == "no-access") {
				return nil, "", invalidExtraConfig("extra_config_reserved_group", name.line)
			}
			if kind.value == "auth-group" {
				if p.auth[name.value] {
					return nil, "", invalidExtraConfig("extra_config_duplicate_name", name.line)
				}
				err = p.authBody()
				p.auth[name.value] = true
			} else {
				if p.groupNames[lower] {
					return nil, "", invalidExtraConfig("extra_config_duplicate_name", name.line)
				}
				p.groupNames[lower] = true
				err = p.groupBody(name.value, kind.value == "transport-group")
			}
		case "lun":
			if _, exists := p.luns[name.value]; exists {
				return nil, "", invalidExtraConfig("extra_config_duplicate_name", name.line)
			}
			var lun targetLUN
			lun, err = p.lunBody(name.value, 0, name.line)
			p.luns[name.value] = lun
			p.state.luns = append(p.state.luns, lun)
		case "target", "controller":
			if p.names[strings.ToLower(name.value)] {
				return nil, "", invalidExtraConfig("extra_config_target_name_conflict", name.line)
			}
			p.names[strings.ToLower(name.value)] = true
			err = p.targetBody(name, kind.value == "controller")
		}
		if err != nil {
			return nil, "", err
		}
		p.semicolon()
	}
	var endpoints []portalEndpoint
	if managed != nil {
		endpoints = append(endpoints, managed.activeEndpoints()...)
	}
	for _, group := range p.state.groups {
		endpoints = append(endpoints, group.listeners...)
	}
	if s.endpointsOverlap(ctx, endpoints) {
		return nil, "", invalidExtraConfig("extra_config_listener_overlap", 0)
	}
	var rendered strings.Builder
	last := 0
	for _, replacement := range p.replacements {
		rendered.WriteString(text[last:replacement.token.start])
		rendered.WriteString(nativeQuote(replacement.value))
		last = replacement.token.end
	}
	rendered.WriteString(text[last:])
	return p.state, rendered.String(), nil
}

func (p *extraConfigParser) authBody() error {
	arities := map[string]int{"auth-type": 1, "chap": 2, "chap-mutual": 4, "host-address": 1, "host-nqn": 1, "initiator-name": 1, "initiator-portal": 1}
	for {
		clause := p.take()
		if !clause.quoted && clause.value == "}" {
			return nil
		}
		arity := arities[clause.value]
		if clause.quoted || arity == 0 {
			return invalidExtraConfig("extra_config_unsupported_clause", clause.line)
		}
		if _, err := p.arguments(arity); err != nil {
			return err
		}
		p.semicolon()
	}
}

func (p *extraConfigParser) groupBody(name string, transport bool) error {
	group := targetGroup{transport: transport, options: make(map[string]string)}
	for {
		clause := p.take()
		if !clause.quoted && clause.value == "}" {
			p.state.groups[name] = group
			return nil
		}
		if clause.quoted {
			return invalidExtraConfig("extra_config_unsupported_clause", clause.line)
		}
		switch clause.value {
		case "listen":
			port := 3260
			if transport {
				protocol := p.take()
				if protocol.quoted || (protocol.value != "tcp" && protocol.value != "discovery-tcp") {
					return invalidExtraConfig("extra_config_unsupported_transport", protocol.line)
				}
				port = 4420
				if protocol.value == "discovery-tcp" {
					port = 8009
				}
			}
			token, err := p.argument()
			if err != nil {
				return err
			}
			endpoint, err := p.service.parseListener(token.value, port)
			if err != nil {
				return invalidExtraConfig("extra_config_invalid_listener", token.line)
			}
			group.listeners = append(group.listeners, endpoint)
			group.rawListeners = append(group.rawListeners, token.value)
			p.replacements = append(p.replacements, listenerReplacement{token: token, value: endpoint.listen()})
		case "discovery-auth-group":
			token, err := p.argument()
			if err != nil {
				return err
			}
			if err := p.authReference(token); err != nil {
				return err
			}
		case "discovery-filter", "pcp", "redirect":
			if transport && clause.value == "redirect" {
				return invalidExtraConfig("extra_config_unsupported_clause", clause.line)
			}
			if _, err := p.argument(); err != nil {
				return err
			}
			if clause.value == "redirect" {
				group.redirect = true
			}
		case "dscp":
			token := p.take()
			if !nativeStringToken(token) && (token.quoted || !(strings.HasPrefix(token.value, "af") || strings.HasPrefix(token.value, "cs") || token.value == "be" || token.value == "ef")) {
				return invalidExtraConfig("invalid_extra_config_syntax", token.line)
			}
		case "option":
			if err := p.option(group.options, true); err != nil {
				return err
			}
		default:
			return invalidExtraConfig("extra_config_unsupported_clause", clause.line)
		}
		p.semicolon()
	}
}

func (p *extraConfigParser) targetBody(name nativeConfigToken, controller bool) error {
	name.value = strings.ToLower(name.value)
	target := targetDefinition{name: name.value, controller: controller, line: name.line}
	groupClause, lunClause, identity := "portal-group", "lun", "lun"
	if controller {
		groupClause, lunClause, identity = "transport-group", "namespace", "nsid"
	}
	seenGroups, seenNumbers := make(map[string]bool), make(map[int]bool)
	for {
		clause := p.take()
		if !clause.quoted && clause.value == "}" {
			if len(target.groups) == 0 {
				return invalidExtraConfig("extra_config_explicit_group_required", name.line)
			}
			p.state.targets = append(p.state.targets, target)
			return nil
		}
		if clause.quoted {
			return invalidExtraConfig("extra_config_unsupported_clause", clause.line)
		}
		switch clause.value {
		case groupClause:
			token, err := p.argument()
			if err != nil {
				return err
			}
			if reservedExtraGroup(token.value) {
				return invalidExtraConfig("extra_config_reserved_group", token.line)
			}
			group, exists := p.state.groups[token.value]
			if !exists || group.transport != controller || seenGroups[token.value] {
				return invalidExtraConfig("extra_config_unknown_listener_group", token.line)
			}
			seenGroups[token.value] = true
			target.groups = append(target.groups, token.value)
			if nativeStringToken(p.peek()) {
				if err := p.authReference(p.take()); err != nil {
					return err
				}
			}
		case lunClause:
			token, err := p.argument()
			if err != nil {
				return err
			}
			number, err := nativeConfigNumber(token.value)
			if err != nil || number > maxTargetLUNNumber || controller && number == 0 || seenNumbers[int(number)] {
				return invalidExtraConfig("extra_config_invalid_lun_number", token.line)
			}
			seenNumbers[int(number)] = true
			var lun targetLUN
			if next := p.peek(); !next.quoted && next.value == "{" {
				p.take()
				lunName := fmt.Sprintf("%s,%s,%d", name.value, identity, number)
				if _, exists := p.luns[lunName]; exists {
					return invalidExtraConfig("extra_config_duplicate_name", token.line)
				}
				lun, err = p.lunBody(lunName, int(number), token.line)
				p.luns[lunName] = lun
			} else {
				ref, refErr := p.argument()
				if refErr != nil {
					return refErr
				}
				var exists bool
				lun, exists = p.luns[ref.value]
				if !exists || lun.line == 0 {
					return invalidExtraConfig("extra_config_unknown_lun", ref.line)
				}
				lun.number = int(number)
			}
			if err != nil {
				return err
			}
			target.luns = append(target.luns, lun)
		case "auth-group":
			token, err := p.argument()
			if err != nil {
				return err
			}
			if err := p.authReference(token); err != nil {
				return err
			}
		case "auth-type", "alias", "redirect", "initiator-name", "initiator-portal", "host-address", "host-nqn", "chap", "chap-mutual":
			allowed := clause.value == "auth-type"
			if controller {
				allowed = allowed || clause.value == "host-address" || clause.value == "host-nqn"
			} else {
				allowed = allowed || clause.value == "alias" || clause.value == "redirect" || clause.value == "initiator-name" || clause.value == "initiator-portal" || clause.value == "chap" || clause.value == "chap-mutual"
			}
			if !allowed {
				return invalidExtraConfig("extra_config_unsupported_clause", clause.line)
			}
			count := 1
			if clause.value == "chap" {
				count = 2
			} else if clause.value == "chap-mutual" {
				count = 4
			}
			values, err := p.arguments(count)
			if err != nil {
				return err
			}
			if clause.value == "alias" && strings.ContainsAny(values[0].value, "<>&") {
				return invalidExtraConfig("extra_config_unobservable_value", values[0].line)
			}
		default:
			return invalidExtraConfig("extra_config_unsupported_clause", clause.line)
		}
		p.semicolon()
	}
}

func (p *extraConfigParser) lunBody(name string, number, line int) (targetLUN, error) {
	lun := targetLUN{name: name, number: number, backend: "block", line: line, options: make(map[string]string)}
	seen := make(map[string]bool)
	for {
		clause := p.take()
		if !clause.quoted && clause.value == "}" {
			if lun.blocksize == 0 {
				lun.blocksize = 512
				if lun.deviceType == 5 {
					lun.blocksize = 2048
				}
			}
			if lun.backend != "block" && lun.backend != "ramdisk" {
				return lun, invalidExtraConfig("extra_config_unsupported_backend", line)
			}
			if lun.backend == "block" && lun.path == "" || lun.backend == "ramdisk" && (lun.path != "" || lun.size == 0) || lun.size%lun.blocksize != 0 {
				return lun, invalidExtraConfig("extra_config_invalid_backing", line)
			}
			return lun, nil
		}
		if clause.quoted || (clause.value != "backend" && clause.value != "blocksize" && clause.value != "device-id" && clause.value != "device-type" && clause.value != "ctl-lun" && clause.value != "option" && clause.value != "path" && clause.value != "serial" && clause.value != "size") {
			return lun, invalidExtraConfig("extra_config_unsupported_clause", clause.line)
		}
		if seen[clause.value] && clause.value != "option" {
			return lun, invalidExtraConfig("extra_config_duplicate_clause", clause.line)
		}
		seen[clause.value] = true
		if clause.value == "option" {
			if err := p.option(lun.options, false); err != nil {
				return lun, err
			}
			p.semicolon()
			continue
		}
		token, err := p.argument()
		if err != nil {
			return lun, err
		}
		switch clause.value {
		case "backend":
			lun.backend = token.value
		case "path":
			if !filepath.IsAbs(token.value) {
				return lun, invalidExtraConfig("extra_config_invalid_backing", token.line)
			}
			if strings.ContainsAny(token.value, "<>&") {
				return lun, invalidExtraConfig("extra_config_unobservable_value", token.line)
			}
			lun.path = token.value
		case "serial":
			if len(token.value) > 16 {
				return lun, invalidExtraConfig("extra_config_native_field_too_long", token.line)
			}
			lun.serial = token.value
		case "device-id":
			if len(token.value) > 64 {
				return lun, invalidExtraConfig("extra_config_native_field_too_long", token.line)
			}
			lun.deviceID = token.value
		case "device-type":
			switch strings.ToLower(token.value) {
			case "disk", "direct":
				lun.deviceType = 0
			case "processor":
				lun.deviceType = 3
			case "cd", "cdrom", "dvd", "dvdrom":
				lun.deviceType = 5
			default:
				value, err := strconv.Atoi(token.value)
				if err != nil || value < 0 || value > 15 {
					return lun, invalidExtraConfig("extra_config_invalid_device_type", token.line)
				}
				lun.deviceType = value
			}
		case "size", "blocksize", "ctl-lun":
			value, err := nativeConfigNumber(token.value)
			if err != nil || clause.value != "ctl-lun" && value == 0 || clause.value != "size" && value > math.MaxUint32 {
				return lun, invalidExtraConfig("extra_config_invalid_number", token.line)
			}
			switch clause.value {
			case "size":
				lun.size = value
			case "blocksize":
				lun.blocksize = value
			case "ctl-lun":
				id := int(value)
				lun.ctlID = &id
			}
		}
		p.semicolon()
	}
}

func (p *extraConfigParser) option(options map[string]string, port bool) error {
	tokens, err := p.arguments(2)
	if err != nil {
		return err
	}
	name, value := tokens[0], tokens[1]
	if !nativeOptionName.MatchString(name.value) || strings.ContainsAny(value.value, "<>&") {
		return invalidExtraConfig("extra_config_unobservable_value", name.line)
	}
	reserved := "ctld_name backend_type lun_type size blocksize serial_number device_id file dev id num_threads"
	if port {
		reserved = "ctld_portal_group_name ctld_transport_group_name cfiscsi_target cfiscsi_target_alias cfiscsi_portal_group_tag nqn subnqn portid id frontend_type online lun_map lun port port_type port_name physical_port virtual_port target initiator host"
	}
	for _, key := range strings.Fields(reserved) {
		if strings.EqualFold(name.value, key) {
			return invalidExtraConfig("extra_config_reserved_option", name.line)
		}
	}
	if _, exists := options[name.value]; exists {
		return invalidExtraConfig("extra_config_duplicate_clause", name.line)
	}
	options[name.value] = value.value
	return nil
}

func nativeConfigNumber(text string) (uint64, error) {
	shift := 0
	if text != "" {
		units := "bkmgtpe"
		if i := strings.IndexByte(units, strings.ToLower(text)[len(text)-1]); i >= 0 {
			shift = i * 10
			text = text[:len(text)-1]
		}
	}
	if strings.Contains(text, "_") {
		return 0, errors.New("invalid_number")
	}
	value, err := strconv.ParseUint(text, 0, 64)
	if err != nil || value > math.MaxUint64>>shift {
		return 0, errors.New("invalid_number")
	}
	return value << shift, nil
}

func (state *targetConfiguration) configuredLUNs() []targetLUN {
	luns := append([]targetLUN(nil), state.luns...)
	seen := make(map[string]bool)
	for _, lun := range luns {
		seen[lun.name] = true
	}
	for _, target := range state.targets {
		for _, lun := range target.luns {
			if !seen[lun.name] {
				seen[lun.name] = true
				luns = append(luns, lun)
			}
		}
	}
	return luns
}

func (state *targetConfiguration) addExtra(extra *targetConfiguration) {
	for name, group := range extra.groups {
		state.groups[name] = group
	}
	state.targets = append(state.targets, extra.targets...)
	state.luns = append(state.luns, extra.luns...)
}

func (s *Service) inspectTargetConfig(text string) (*targetConfiguration, error) {
	generated, extra, hasExtra := strings.Cut(text, extraConfigBanner+"\n")
	state, err := s.inspectManagedTargetConfig(generated)
	if err != nil || !hasExtra {
		return state, err
	}
	ctx, cancel := s.targetContext()
	defer cancel()
	user, _, err := s.parseExtraTargetConfigText(ctx, extra, state)
	if err != nil {
		return nil, err
	}
	state.addExtra(user)
	return state, nil
}

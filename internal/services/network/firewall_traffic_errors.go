// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import "errors"

const (
	errHiddenFirewallRuleMutation = "hidden_firewall_rule_managed_by_wireguard"

	MaxFirewallTrafficRuleNameBytes        = 128
	MaxFirewallTrafficRuleDescriptionBytes = 2048
	MaxFirewallTrafficRuleInterfaces       = 64
	MaxFirewallTrafficRuleInterfaceBytes   = 64
	MaxFirewallTrafficRuleSelectorBytes    = 2048
	MaxFirewallTrafficRulePriority         = 1_000_000
	MaxFirewallTrafficRuleReorderItems     = 1024
	MaxFirewallTrafficRuleDeleteItems      = 1024
)

var (
	ErrInvalidFirewallTrafficRule  = errors.New("invalid firewall traffic rule")
	ErrFirewallTrafficRuleNotFound = errors.New("firewall traffic rule not found")
	ErrFirewallTrafficRuleConflict = errors.New("firewall traffic rule conflict")
	ErrHiddenFirewallRuleMutation  = errors.New(errHiddenFirewallRuleMutation)
)

func invalidFirewallTrafficRule(cause error) error {
	if cause == nil || errors.Is(cause, ErrInvalidFirewallTrafficRule) {
		return ErrInvalidFirewallTrafficRule
	}
	return errors.Join(ErrInvalidFirewallTrafficRule, cause)
}

func firewallTrafficRuleNotFound(cause error) error {
	if cause == nil {
		return ErrFirewallTrafficRuleNotFound
	}
	return errors.Join(ErrFirewallTrafficRuleNotFound, cause)
}

func firewallTrafficRuleConflict(cause error) error {
	if cause == nil {
		return ErrFirewallTrafficRuleConflict
	}
	return errors.Join(ErrFirewallTrafficRuleConflict, cause)
}

// FirewallTrafficRuleErrorCode returns a stable code suitable for an API response.
func FirewallTrafficRuleErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrHiddenFirewallRuleMutation):
		return "hidden_firewall_rule_managed_by_wireguard"
	case errors.Is(err, ErrInvalidFirewallTrafficRule):
		return "invalid_firewall_traffic_rule"
	case errors.Is(err, ErrFirewallTrafficRuleNotFound):
		return "firewall_traffic_rule_not_found"
	case errors.Is(err, ErrFirewallTrafficRuleConflict):
		return "firewall_traffic_rule_conflict"
	default:
		return "firewall_traffic_rule_operation_failed"
	}
}

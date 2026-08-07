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
	MaxFirewallNATRuleNameBytes          = 128
	MaxFirewallNATRuleDescriptionBytes   = 2048
	MaxFirewallNATRuleInterfaces         = 64
	MaxFirewallNATRuleInterfaceBytes     = 64
	MaxFirewallNATRuleSelectorBytes      = 2048
	MaxFirewallNATRulePolicyGatewayBytes = 64
	MaxFirewallNATRulePriority           = 1_000_000
	MaxFirewallNATRuleReorderItems       = 1024
)

var (
	ErrInvalidFirewallNATRule  = errors.New("invalid firewall NAT rule")
	ErrFirewallNATRuleNotFound = errors.New("firewall NAT rule not found")
	ErrFirewallNATRuleConflict = errors.New("firewall NAT rule conflict")
)

func invalidFirewallNATRule(cause error) error {
	if cause == nil || errors.Is(cause, ErrInvalidFirewallNATRule) {
		return ErrInvalidFirewallNATRule
	}
	return errors.Join(ErrInvalidFirewallNATRule, cause)
}

func firewallNATRuleNotFound(cause error) error {
	if cause == nil {
		return ErrFirewallNATRuleNotFound
	}
	return errors.Join(ErrFirewallNATRuleNotFound, cause)
}

func firewallNATRuleConflict(cause error) error {
	if cause == nil {
		return ErrFirewallNATRuleConflict
	}
	return errors.Join(ErrFirewallNATRuleConflict, cause)
}

// FirewallNATRuleErrorCode returns a stable code suitable for an API response.
func FirewallNATRuleErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrHiddenFirewallRuleMutation):
		return "hidden_firewall_rule_managed_by_wireguard"
	case errors.Is(err, ErrInvalidFirewallNATRule):
		return "invalid_firewall_nat_rule"
	case errors.Is(err, ErrFirewallNATRuleNotFound):
		return "firewall_nat_rule_not_found"
	case errors.Is(err, ErrFirewallNATRuleConflict):
		return "firewall_nat_rule_conflict"
	default:
		return "firewall_nat_rule_operation_failed"
	}
}

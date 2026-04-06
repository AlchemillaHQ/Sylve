// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

func (s *Service) collectTrafficCountersFromPF() (map[uint]trafficRuleCounterTotals, map[int]uint, error) {
	out, err := firewallRunCommand("/sbin/pfctl", "-a", "sylve/traffic-rules", "-vvsr")
	if err != nil {
		return nil, nil, err
	}
	totals, numbers := parseLabeledRuleCounters(out, pfTrafficRuleLabelPattern)
	return totals, numbers, nil
}

func (s *Service) collectNATCountersFromPF() (map[uint]trafficRuleCounterTotals, map[int]uint, error) {
	out, err := firewallRunCommand("/sbin/pfctl", "-a", "sylve/nat-rules", "-vvsn")
	if err != nil {
		return nil, nil, err
	}
	totals, numbers := parseLabeledRuleCounters(out, pfNATRuleLabelPattern)
	return totals, numbers, nil
}

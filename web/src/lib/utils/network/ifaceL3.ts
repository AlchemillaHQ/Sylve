// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

const CONFLICT_LABELS: Record<string, string> = {
	host_interface_l3_missing_interface: 'Interface is missing',
	host_interface_l3_identity_mismatch: 'Hardware MAC differs from the adopted interface',
	host_interface_l3_standard_switch_port: 'Interface is a standard switch port',
	host_interface_l3_bridge_member: 'Interface is a bridge member',
	host_interface_l3_vlan_parent_missing: 'VLAN parent interface is missing',
	host_interface_l3_vlan_parent_ineligible: 'VLAN parent is not eligible for Host IP',
	host_interface_l3_parent_has_host_ip: 'Parent interface already has Host IP configuration',
	host_interface_l3_parent_has_vlan_children: 'Interface already has VLAN children',
	host_interface_l3_ineligible_no_mac: 'Interface has no MAC address',
	host_interface_l3_ineligible_no_driver: 'Interface has no hardware driver'
};

export function hostInterfaceL3Label(code: string): string {
	if (!code) {
		return '';
	}
	const known = CONFLICT_LABELS[code];
	if (known) {
		return known;
	}
	if (code.startsWith('host_interface_l3_ineligible_')) {
		const kind = code.replace('host_interface_l3_ineligible_', '');
		return `Interface type is not supported (${kind})`;
	}
	return code;
}

export function hostInterfaceL3Labels(codes: string[] | undefined): string[] {
	return (codes ?? []).map(hostInterfaceL3Label).filter((label) => label !== '');
}

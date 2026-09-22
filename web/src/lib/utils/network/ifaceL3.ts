// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

import { Address6 } from 'ip-address';
import type { HostInterfaceL3Entry, HostInterfaceL3Target } from '$lib/types/network/ifaceL3';

const CONFLICT_LABELS: Record<string, string> = {
	host_interface_l3_missing_interface: 'Interface is missing',
	host_interface_l3_identity_mismatch: 'Hardware MAC differs from the adopted interface',
	host_interface_l3_standard_switch_port: 'Interface is a standard switch port',
	host_interface_l3_bridge_member: 'Interface is a bridge member',
	host_interface_l3_vlan_parent_missing: 'VLAN parent interface is missing',
	host_interface_l3_vlan_parent_ineligible: 'VLAN parent is not eligible for Host IP',
	host_interface_l3_parent_has_host_ip: 'Parent interface already has Host IP configuration',
	host_interface_l3_parent_has_vlan_children: 'Interface already has VLAN children',
	host_interface_l3_prefix_owner_changed: 'IPv4 prefix ownership changed; save before reapplying',
	host_interface_l3_vlan_identity_mismatch: 'VLAN parent or tag differs from the adopted interface',
	host_interface_l3_pending_conflict: 'A Host IP change is awaiting confirmation',
	host_interface_l3_ineligible_no_mac: 'Interface has no MAC address',
	host_interface_l3_ineligible_no_driver: 'Interface has no physical hardware metadata'
};

const ERROR_MESSAGES: Record<string, string> = {
	host_interface_l3_invalid_address: 'Enter a usable host address in CIDR form',
	host_interface_l3_duplicate_address: 'That address is already configured',
	host_interface_l3_address_in_dhcp_pool: 'That address falls inside a configured DHCP range',
	host_interface_l3_invalid_mtu: 'MTU must be between 68 and 65535',
	host_interface_l3_mtu_below_ipv6_floor: 'MTU must be at least 1280 while IPv6 is enabled',
	host_interface_l3_invalid_metric: 'Metric must be between 0 and 255',
	host_interface_l3_invalid_ipv6_mode: 'Unsupported IPv6 mode',
	host_interface_l3_ipv6_address_with_disabled:
		'Remove the static IPv6 addresses before disabling IPv6',
	host_interface_l3_child_mtu_above_parent: 'MTU cannot exceed the parent interface MTU',
	host_interface_l3_revision_mismatch: 'The configuration changed elsewhere',
	host_interface_l3_pending_conflict: 'A Host IP change is awaiting confirmation',
	host_interface_l3_apply_pending: 'The change is still being applied',
	host_interface_l3_delete_pending: 'A removal is awaiting confirmation',
	host_interface_l3_confirmation_expired:
		'The confirmation window expired and the change was rolled back',
	host_interface_l3_invalid_interface: 'Invalid interface name',
	host_interface_l3_not_found: 'No Host IP configuration exists for this interface',
	host_interface_l3_local_node_only: 'Host IP changes must be made on the node itself',
	host_interface_l3_operation_failed: 'The Host IP operation failed'
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

export function hostInterfaceL3Message(code: string): string {
	if (!code) {
		return '';
	}
	return ERROR_MESSAGES[code] ?? hostInterfaceL3Label(code);
}

export type NormalizedHostAddress = {
	family: 'inet' | 'inet6';
	host: string;
};

export function normalizeHostAddress(value: string): NormalizedHostAddress | null {
	const hostPart = value.trim().split('/')[0] ?? '';
	if (hostPart === '') {
		return null;
	}

	if (hostPart.includes(':')) {
		if (!Address6.isValid(hostPart)) {
			return null;
		}
		try {
			const parsed = new Address6(hostPart);
			if (parsed.is4()) {
				return { family: 'inet', host: parsed.to4().correctForm() };
			}
			return { family: 'inet6', host: parsed.correctForm().toLowerCase() };
		} catch {
			return null;
		}
	}

	const parts = hostPart.split('.');
	if (
		parts.length !== 4 ||
		parts.some((part) => !/^\d{1,3}$/.test(part) || (part.length > 1 && part.startsWith('0')))
	) {
		return null;
	}
	const octets = parts.map(Number);
	if (octets.some((octet) => octet > 255)) {
		return null;
	}
	return { family: 'inet', host: octets.join('.') };
}

export type HostInterfaceL3PreflightMode = 'save' | 'reapply';

export type HostInterfaceL3PreflightInput = {
	mode: HostInterfaceL3PreflightMode;
	row: HostInterfaceL3Entry | null;
	target: HostInterfaceL3Target | null;
	pendingCount: number;
	addressOwners: Record<string, string>;
	requestedAddresses: string[];
};

export type HostInterfaceL3PreflightResult =
	| { status: 'ok' }
	| { status: 'pending'; count: number }
	| { status: 'blocked'; title: string; description?: string };

export function hostInterfaceL3Preflight(
	input: HostInterfaceL3PreflightInput
): HostInterfaceL3PreflightResult {
	const title = input.mode === 'reapply' ? 'Cannot reapply' : 'Cannot save';

	if (input.row && !input.row.present) {
		return {
			status: 'blocked',
			title: 'Interface is missing',
			description: 'The configuration is kept so it can still be removed.'
		};
	}

	if (input.pendingCount > 0) {
		return { status: 'pending', count: input.pendingCount };
	}

	const blockingConflicts = (input.row?.conflicts ?? []).filter(
		(conflict) => input.mode === 'reapply' || conflict !== 'host_interface_l3_prefix_owner_changed'
	);
	if (blockingConflicts.length > 0) {
		const description = blockingConflicts
			.map(hostInterfaceL3Message)
			.filter((message) => message !== '')
			.join('; ');
		if (description !== '') {
			return { status: 'blocked', title, description };
		}
	}

	if (input.target && !input.target.eligible && input.target.reason) {
		const description = hostInterfaceL3Message(input.target.reason);
		if (description !== '') {
			return { status: 'blocked', title, description };
		}
	}

	if (input.mode === 'save') {
		for (const address of input.requestedAddresses) {
			const normalized = normalizeHostAddress(address);
			if (!normalized) {
				continue;
			}
			const owner = input.addressOwners[`${normalized.family}|${normalized.host}`];
			if (owner) {
				return {
					status: 'blocked',
					title: 'Address in use',
					description: `${address} is already configured on ${owner}`
				};
			}
		}
	}

	return { status: 'ok' };
}

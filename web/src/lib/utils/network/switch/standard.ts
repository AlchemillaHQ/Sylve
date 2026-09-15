/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { Column, Row } from '$lib/types/components/tree-table';
import type { NetworkObject } from '$lib/types/network/object';
import type { StandardSwitchVLANConfig } from '$lib/api/network/switch';
import type { SwitchList, VLANPortPolicy } from '$lib/types/network/switch';
import { parseOptionalVLAN, parseTaggedVLANs } from '$lib/utils/network/vlan';
import { escapeHTML } from '$lib/utils/string';
import type { CellComponent } from 'tabulator-tables';
import { renderWithIcon } from '../../table';

export interface VLANPolicyDraft {
	mode: VLANPortPolicy['mode'];
	untaggedVlan: string | number;
	taggedVlans: string;
}

export type VLANConfigResult =
	| { ok: true; value: StandardSwitchVLANConfig }
	| { ok: false; error: string };

export function buildVLANConfig(
	filtering: boolean,
	defaultAccessDraft: string | number,
	hostDraft: string | number,
	ports: string[],
	drafts: Record<string, VLANPolicyDraft>
): VLANConfigResult {
	if (!filtering) {
		return {
			ok: true,
			value: { vlanFiltering: false, defaultAccessVlan: null, hostVlan: null, portPolicies: {} }
		};
	}

	const defaultAccessVlan = parseOptionalVLAN(defaultAccessDraft);
	if (Number.isNaN(defaultAccessVlan)) {
		return { ok: false, error: 'Default access VLAN must be between 1 and 4094' };
	}
	const hostVlan = parseOptionalVLAN(hostDraft);
	if (Number.isNaN(hostVlan)) {
		return { ok: false, error: 'Host VLAN must be between 1 and 4094' };
	}

	const portPolicies: Record<string, VLANPortPolicy> = {};
	for (const port of [...ports].sort()) {
		const draft = drafts[port];
		if (!draft || (draft.mode !== 'access' && draft.mode !== 'trunk')) {
			return { ok: false, error: `Choose an access or trunk policy for ${port}` };
		}

		const untaggedVlan = parseOptionalVLAN(draft.untaggedVlan);
		if (Number.isNaN(untaggedVlan)) {
			return { ok: false, error: `${port}: VLAN IDs must be between 1 and 4094` };
		}
		if (draft.mode === 'access') {
			if (untaggedVlan === null) {
				return { ok: false, error: `${port}: an access VLAN is required` };
			}
			portPolicies[port] = { mode: 'access', untaggedVlan, taggedVlans: [] };
			continue;
		}

		const taggedVlans = parseTaggedVLANs(draft.taggedVlans);
		if (!taggedVlans || taggedVlans.length === 0) {
			return { ok: false, error: `${port}: enter at least one tagged VLAN or VLAN range` };
		}
		if (untaggedVlan !== null && taggedVlans.includes(untaggedVlan)) {
			return { ok: false, error: `${port}: the native VLAN cannot also be tagged` };
		}
		portPolicies[port] = {
			mode: 'trunk',
			...(untaggedVlan === null ? {} : { untaggedVlan }),
			taggedVlans
		};
	}

	return {
		ok: true,
		value: { vlanFiltering: true, defaultAccessVlan, hostVlan, portPolicies }
	};
}

export interface SwitchPortPolicySource {
	name: string;
	vlanPolicy?: VLANPortPolicy;
}

const ACCESS_BADGE_COLORS = 'text-cyan-400 border-cyan-400/50';
const TRUNK_BADGE_COLORS = 'text-amber-400 border-amber-400/50';
const DEFAULT_ACCESS_BADGE_COLORS = 'text-violet-400 border-violet-400/50';
const MAX_VISIBLE_PORT_LINES = 4;
const MAX_VISIBLE_TAGGED_VLANS = 8;

function vlanBadge(label: string, colors: string, title?: string): string {
	const titleAttribute = title ? ` title="${title}"` : '';
	return `<span${titleAttribute} class="inline-flex items-center font-mono text-xs px-1 rounded border ${colors} leading-tight">${label}</span>`;
}

function portPolicyText(port: SwitchPortPolicySource): string {
	const policy = port.vlanPolicy;
	if (policy?.mode === 'access') {
		return `${port.name} access ${policy.untaggedVlan ?? '-'}`;
	}
	if (policy?.mode === 'trunk') {
		const native = policy.untaggedVlan === undefined ? '' : ` native ${policy.untaggedVlan}`;
		const tagged = (policy.taggedVlans ?? []).join(' ');
		return `${port.name} trunk${native}${tagged ? ` tagged ${tagged}` : ''}`;
	}
	return port.name;
}

function formatPortPolicy(port: SwitchPortPolicySource): string {
	const parts = [`<span>${escapeHTML(port.name)}</span>`];
	const policy = port.vlanPolicy;
	const taggedVlans = policy?.taggedVlans ?? [];

	if (policy?.mode === 'access') {
		parts.push(
			vlanBadge(`ACCESS ${policy.untaggedVlan ?? '-'}`, ACCESS_BADGE_COLORS, 'Untagged access VLAN')
		);
	} else if (policy?.mode === 'trunk') {
		parts.push(vlanBadge('TRUNK', TRUNK_BADGE_COLORS, 'Tagged trunk with an optional native VLAN'));
		if (policy.untaggedVlan !== undefined) {
			parts.push(
				`<span class="font-mono text-xs" title="Native (untagged) VLAN">native ${policy.untaggedVlan}</span>`
			);
		}
		if (taggedVlans.length > 0) {
			const visible = taggedVlans.slice(0, MAX_VISIBLE_TAGGED_VLANS);
			const hiddenCount = taggedVlans.length - visible.length;
			const hiddenSuffix = hiddenCount > 0 ? ` +${hiddenCount}` : '';
			const taggedTitle =
				hiddenCount > 0 ? `Allowed tagged VLANs: ${taggedVlans.join(',')}` : 'Allowed tagged VLANs';
			parts.push(
				`<span class="font-mono text-xs" title="${escapeHTML(taggedTitle)}">${escapeHTML(visible.join(','))}${hiddenSuffix}</span>`
			);
		}
	}

	const separator = '<span class="text-muted-foreground/50">·</span>';
	return `<span class="inline-flex items-center gap-1.5">${parts.join(separator)}</span>`;
}

function formatPortsCell(ports: SwitchPortPolicySource[]): string {
	const visible = ports.slice(0, MAX_VISIBLE_PORT_LINES);
	const hidden = ports.slice(MAX_VISIBLE_PORT_LINES);
	const lines = visible.map((port) => formatPortPolicy(port)).join('<br/>');
	if (hidden.length === 0) return lines;

	const hiddenDetail = escapeHTML(hidden.map((port) => portPolicyText(port)).join(', '));
	return `${lines}<br/><span class="text-muted-foreground text-xs" title="${hiddenDetail}">+${hidden.length} more</span>`;
}

export function generateTableData(switches: SwitchList | undefined): {
	rows: Row[];
	columns: Column[];
} {
	const columns: Column[] = [
		{
			field: 'id',
			visible: false,
			title: 'ID'
		},
		{
			field: 'name',
			title: 'Name',
			formatter(cell: CellComponent) {
				const value = cell.getValue();
				const row = cell.getRow();
				const data = row.getData();
				const pSw = data.private || false;

				if (pSw) {
					return renderWithIcon('material-symbols-light:private-connectivity-outline', value);
				}

				return renderWithIcon('mdi:public', value);
			}
		},
		{
			field: 'ports',
			title: 'Ports',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				if (!value || !Array.isArray(value) || value.length === 0) return '-';

				const ports = value as SwitchPortPolicySource[];
				if (!cell.getRow().getData().vlanFiltering) {
					return ports.map((port) => `<span>${escapeHTML(port.name)}</span>`).join(', ');
				}

				return formatPortsCell(ports);
			}
		},
		{
			field: 'mtu',
			title: 'MTU',
			sorter: 'number'
		},
		{
			field: 'vlanFiltering',
			title: 'Filtering',
			formatter: (cell: CellComponent) =>
				cell.getValue()
					? renderWithIcon('mdi:filter-variant', 'Filtered', 'text-emerald-400')
					: renderWithIcon('mdi:filter-off-outline', 'Unfiltered', 'text-muted-foreground')
		},
		{
			field: 'hostVlan',
			title: 'Host VLAN',
			sorter: 'number',
			formatter: (cell: CellComponent) => {
				const data = cell.getRow().getData();
				const value = cell.getValue() as number | null;
				if (!data.vlanFiltering || value === null || value === undefined) return '-';

				const hostInterface = data.bridgeName ? `${data.bridgeName}.${value}` : '';
				return renderWithIcon(
					'mdi:bridge',
					String(value),
					'text-emerald-400',
					escapeHTML(hostInterface) || 'Host VLAN interface'
				);
			}
		},
		{
			field: 'defaultAccessVlan',
			title: 'Default access VLAN',
			sorter: 'number',
			formatter: (cell: CellComponent) => {
				const data = cell.getRow().getData();
				const value = cell.getValue() as number | null;
				if (!data.vlanFiltering) return '-';
				if (value === null || value === undefined) {
					return renderWithIcon(
						'mdi:lan-disconnect',
						'Rejected',
						'text-muted-foreground',
						'New members, including VM TAPs, are rejected'
					);
				}

				return vlanBadge(
					String(value),
					DEFAULT_ACCESS_BADGE_COLORS,
					'New members, including VM TAPs, inherit this VLAN'
				);
			}
		},
		{
			field: 'vlan',
			title: 'VLAN child',
			formatter: (cell: CellComponent) =>
				cell.getRow().getData().vlanFiltering ? '-' : cell.getValue()
		},
		{
			field: 'ipv4',
			title: 'IPv4',
			formatter: (cell: CellComponent) => {
				const row = cell.getRow();
				const data = row.getData();

				if (data.dhcp) {
					return 'DHCP';
				}

				let v4 = '';
				let gw4 = '';

				const networkObj = data.networkObj as NetworkObject;
				if (networkObj && networkObj.entries && networkObj.entries.length > 0) {
					v4 = networkObj.entries[0].value || '-';
				} else if (data.networkManual) {
					v4 = data.networkManual as string;
				} else {
					v4 = '-';
				}

				const gatewayObj = data.gatewayAddressObj as NetworkObject;
				if (gatewayObj && gatewayObj.entries && gatewayObj.entries.length > 0) {
					gw4 = gatewayObj.entries[0].value || '-';
				} else if (data.gatewayManual) {
					gw4 = data.gatewayManual as string;
				} else {
					gw4 = '-';
				}

				return (
					[v4, gw4]
						.filter((address) => address !== '-')
						.map((address) => `<span>${address}</span>`)
						.join('<br/>') || '-'
				);
			}
		},
		{
			field: 'ipv6',
			title: 'IPv6',
			formatter: (cell: CellComponent) => {
				const row = cell.getRow();
				const data = row.getData();
				const value = cell.getValue();

				if (value === '-' && data.slaac) {
					return 'SLAAC';
				}

				let v6 = '';
				let gw6 = '';

				const networkObj = data.network6Obj as NetworkObject;
				if (networkObj && networkObj.entries && networkObj.entries.length > 0) {
					v6 = networkObj.entries[0].value || '-';
				} else if (data.network6Manual) {
					v6 = data.network6Manual as string;
				} else {
					v6 = '-';
				}

				const gatewayObj = data.gateway6AddressObj as NetworkObject;
				if (gatewayObj && gatewayObj.entries && gatewayObj.entries.length > 0) {
					gw6 = gatewayObj.entries[0].value || '-';
				} else if (data.gateway6Manual) {
					gw6 = data.gateway6Manual as string;
				} else {
					gw6 = '-';
				}

				return (
					[v6, gw6]
						.filter((address) => address !== '-')
						.map((address) => `<span>${address}</span>`)
						.join('<br/>') || '-'
				);
			}
		},
		{
			field: 'private',
			title: 'Private',
			visible: false
		},
		{
			field: 'dhcp',
			title: 'DHCP',
			visible: false
		},
		{
			field: 'disableIPv6',
			title: 'Disable IPv6',
			visible: false
		},
		{
			field: 'slaac',
			title: 'SLAAC',
			visible: false
		},
		{
			field: 'defaultRoute',
			title: 'IPv4 Default Route',
			visible: false,
			formatter: (cell: CellComponent) => {
				const row = cell.getRow();
				const data = row.getData();

				if (data.defaultRoute) {
					return renderWithIcon('lets-icons:check-fill', 'Yes');
				}

				return renderWithIcon('gridicons:cross-circle', 'No');
			}
		},
		{
			field: 'defaultRoute6',
			title: 'IPv6 Default Route',
			visible: false,
			formatter: (cell: CellComponent) => {
				const row = cell.getRow();
				const data = row.getData();

				if (data.defaultRoute6) {
					return renderWithIcon('lets-icons:check-fill', 'Yes');
				}

				return renderWithIcon('gridicons:cross-circle', 'No');
			}
		},
		{
			field: 'disableBridgeOffloads',
			title: 'Disable Bridge Offloads',
			visible: false
		}
	];

	const rows: Row[] = [];

	if (switches && switches['standard']) {
		for (const sw of switches['standard']) {
			const portsOnly =
				sw.ports?.map((port) => {
					return port.name;
				}) || [];

			rows.push({
				id: sw.id,
				name: sw.name,
				bridgeName: sw.bridgeName,
				mtu: sw.mtu,
				vlan: sw.vlan || '-',
				ipv4: sw.address || '-',
				ipv6: sw.address6 || '-',
				addressObj: sw.addressObj || '-',
				address6Obj: sw.address6Obj || '-',
				networkObj: sw.networkObj || '-',
				gatewayAddressObj: sw.gatewayAddressObj || '-',
				network6Obj: sw.network6Obj || '-',
				gateway6AddressObj: sw.gateway6AddressObj || '-',
				networkManual: sw.networkManual || '',
				gatewayManual: sw.gatewayManual || '',
				network6Manual: sw.network6Manual || '',
				gateway6Manual: sw.gateway6Manual || '',
				ports: sw.ports,
				portPoliciesText: (sw.ports ?? []).map((port) => portPolicyText(port)).join(', '),
				bridgeMacMode: sw.bridgeMacMode,
				bridgeMacSourcePort: sw.bridgeMacSourcePort,
				bridgeMacObjectId: sw.bridgeMacObjectId,
				bridgeMacObject: sw.bridgeMacObject,
				private: sw.private,
				portsOnly: portsOnly,
				dhcp: sw.dhcp || false,
				disableIPv6: sw.disableIPv6 || false,
				slaac: sw.slaac || false,
				defaultRoute: sw.defaultRoute || false,
				defaultRoute6: sw.defaultRoute6 || false,
				disableBridgeOffloads: sw.disableBridgeOffloads || false,
				vlanFiltering: sw.vlanFiltering || false,
				defaultAccessVlan: sw.defaultAccessVlan,
				hostVlan: sw.hostVlan
			});
		}
	}

	return {
		rows: rows,
		columns: columns
	};
}

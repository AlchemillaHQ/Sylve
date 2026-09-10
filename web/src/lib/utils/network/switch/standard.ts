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
import type { CellComponent } from 'tabulator-tables';
import { renderWithIcon } from '../../table';

export interface VLANPolicyDraft {
	mode: VLANPortPolicy['mode'];
	untaggedVlan: string;
	taggedVlans: string;
}

export type VLANConfigResult =
	| { ok: true; value: StandardSwitchVLANConfig }
	| { ok: false; error: string };

export function buildVLANConfig(
	filtering: boolean,
	defaultAccessDraft: string,
	hostDraft: string,
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
					return renderWithIcon('material-symbols-light--private-connectivity-outline', value);
				}

				return renderWithIcon('mdi:public', value);
			}
		},
		{
			field: 'ports',
			title: 'Ports',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				if (value && Array.isArray(value) && value.length > 0) {
					return value.map((port) => `<span>${port.name}</span>`).join(', ');
				}

				return '-';
			}
		},
		{
			field: 'mtu',
			title: 'MTU'
		},
		{
			field: 'vlan',
			title: 'VLAN'
		},
		{
			field: 'vlanFiltering',
			title: 'Filtering',
			formatter: (cell: CellComponent) => (cell.getValue() ? 'Filtered' : 'Unfiltered')
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

				if (v4 !== '-' && gw4 !== '-') {
					return `<span>${v4}</span><br/><span>${gw4}</span>`;
				} else {
					return '-';
				}
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

				if (v6 !== '-' && gw6 !== '-') {
					return `<span>${v6}</span><br/><span>${gw6}</span>`;
				} else {
					return '-';
				}
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

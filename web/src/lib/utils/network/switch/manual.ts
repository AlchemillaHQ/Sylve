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
import type { ManualSwitchRow, SwitchList } from '$lib/types/network/switch';
import { defaultAccessVlanBadge } from '$lib/utils/network/vlan-format';
import { escapeHTML } from '$lib/utils/string';
import { renderWithIcon } from '../../table';

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
			title: 'Name'
		},
		{
			field: 'bridge',
			title: 'Bridge'
		},
		{
			field: 'vlanFiltering',
			title: 'Filtering',
			formatter: (cell) => {
				const row = cell.getRow().getData() as ManualSwitchRow;
				if (!row.vlanStateAvailable) {
					return renderWithIcon(
						'mdi:alert-circle-outline',
						'Unavailable',
						'text-amber-400',
						row.vlanStateError || 'Sylve could not inspect this bridge on the host'
					);
				}

				return cell.getValue()
					? renderWithIcon('mdi:filter-variant', 'Filtered', 'text-emerald-400')
					: renderWithIcon('mdi:filter-off-outline', 'Unfiltered', 'text-muted-foreground');
			}
		},
		{
			field: 'defaultAccessVlan',
			title: 'Default access VLAN',
			sorter: 'number',
			formatter: (cell) => {
				const row = cell.getRow().getData() as ManualSwitchRow;
				if (!row.vlanStateAvailable) {
					const reason = row.vlanStateError || 'Sylve could not inspect this bridge on the host';
					return `<span class="text-muted-foreground cursor-help" title="${escapeHTML(reason)}">-</span>`;
				}

				const value = cell.getValue() as number | null;
				if (value === null || value === undefined) return '-';

				return defaultAccessVlanBadge(value, 'New members, including VM TAPs, inherit this VLAN');
			}
		}
	];

	const rows: Row[] = [];
	if (switches && switches['manual']) {
		for (const sw of switches['manual']) {
			const row: ManualSwitchRow = {
				id: sw.id,
				name: sw.name,
				bridge: sw.bridge || '-',
				vlanFiltering: sw.vlanFiltering,
				defaultAccessVlan: sw.defaultAccessVlan,
				vlanStateAvailable: sw.vlanStateAvailable,
				vlanStateError: sw.vlanStateError
			};

			rows.push(row);
		}
	}

	return { rows, columns };
}

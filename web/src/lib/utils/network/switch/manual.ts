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
				const row = cell.getRow().getData() as { vlanStateAvailable: boolean };
				return row.vlanStateAvailable
					? cell.getValue()
						? 'Filtered'
						: 'Unfiltered'
					: 'Unavailable';
			}
		},
		{
			field: 'defaultAccessVlan',
			title: 'Default access VLAN',
			formatter: (cell) => {
				const row = cell.getRow().getData() as { vlanStateAvailable: boolean };
				return row.vlanStateAvailable ? (cell.getValue() ?? '-') : 'Unavailable';
			}
		},
		{
			field: 'vlanStateAvailable',
			title: 'Runtime state',
			formatter: (cell) =>
				cell.getValue()
					? 'Available'
					: '<span title="Sylve could not inspect this bridge on the host">Unavailable</span>'
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

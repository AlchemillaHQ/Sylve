/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { Row } from '$lib/types/components/tree-table';
import humanFormat, { type ScaleLike } from 'human-format';
import { deepEqual } from 'fast-equals';
import {
	TabulatorFull,
	type CellComponent,
	type RowComponent,
	type Tabulator
} from 'tabulator-tables';

export function deselectAllRows(table: Tabulator | null) {
	if (table) {
		table.getRows().forEach((row) => {
			row.deselect();
		});
	}
}

export function selectOneRow(table: Tabulator | null, row: RowComponent) {
	if (table) {
		deselectAllRows(table);
		row.select();
	}
}

export function getTable(id: string): Tabulator | null {
	const table = TabulatorFull.findTable(`#${id}`);
	if (table) {
		return table[0];
	}

	return null;
}

export function deleteRowByFieldValue(tableId: string, field: string, value: string | number) {
	try {
		const table = getTable(tableId);
		if (table) {
			const rows = table.getRows();
			for (const row of rows) {
				if (row.getData()[field] === value) {
					row.delete();
				}

				if (row.getData()['children']) {
					if (Array.isArray(row.getData()['children'])) {
						const children = row.getData()['children'];
						for (const child of children) {
							if (child[field] === value) {
								row.delete();
							}
						}
					}
				}
			}
		}
	} catch (error) {
		console.error('Error deleting row:', error);
	}
}

export const renderWithIcon = (
	iconKey: string,
	suffix: string,
	extraClass?: string,
	title?: string
) => {
	const [set, icon] = iconKey.split(':');

	return `
		<span class="inline-flex items-center gap-1" title="${title || ''}">
			<span class="icon-[${set}--${icon}] shrink-0 h-3.5 w-3.5  align-middle translate-y-px ${extraClass || ''}"></span>
			  <span class="leading-none">${suffix}</span>
		</span>
	`.trim();
};

export function sizeFormatter(cell: CellComponent) {
	try {
		const sizeOptions = {
			scale: 'binary' as ScaleLike,
			unit: 'B',
			maxDecimals: 1
		};

		return humanFormat(cell.getValue(), sizeOptions);
	} catch (e) {
		return cell.getValue();
	}
}

export function addTabulatorFilters() {
	TabulatorFull.extendModule('filter', 'filters', {
		matchAny
	});
}

export function matchAny(data: any, filterParams: any): boolean {
	const query = filterParams.query?.toString().toLowerCase();
	if (!query) return false;

	function recursiveMatch(obj: any): boolean {
		for (let key in obj) {
			const value = obj[key];

			if (value == null) continue;

			if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
				if (value.toString().toLowerCase().includes(query)) {
					return true;
				}
			}

			if (key === 'children' && Array.isArray(value)) {
				for (const child of value) {
					if (recursiveMatch(child)) return true;
				}
			}

			if (typeof value === 'object' && key !== 'children') {
				if (recursiveMatch(value)) return true;
			}
		}
		return false;
	}

	return recursiveMatch(data);
}

function cleanChildren<T extends { children?: T[] | null }>(row: T): T {
	if (!Array.isArray(row.children)) {
		const { children, ...rest } = row;
		return rest as T;
	}

	const children = row.children.map(cleanChildren).filter(Boolean);

	if (children.length === 0) {
		const { children: _, ...rest } = row;
		return rest as T;
	}

	return { ...row, children };
}

export function hasRowsChanged(table: Tabulator | null, newData: Row[]): boolean {
	if (!table) return true;

	const current = table.getData();
	if (current.length !== newData.length) return true;

	for (let i = 0; i < current.length; i++) {
		if (!deepEqual(cleanChildren(current[i]), cleanChildren(newData[i]))) {
			return true;
		}
	}

	return false;
}

export const restoreTreeState = (expandMap: Map<number, boolean>, rows: RowComponent[]) => {
	for (const row of rows) {
		const id = row.getData().id;

		const expanded = expandMap.get(id);
		if (expanded !== undefined) {
			expanded ? row.treeExpand() : row.treeCollapse();
		}

		const children = row.getTreeChildren();
		if (children.length) {
			restoreTreeState(expandMap, children);
		}
	}
};

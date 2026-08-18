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
import type { RowComponent } from 'tabulator-tables';

export function cleanChildren(row: Row): Row {
	const newRow = { ...row };

	if (Array.isArray(newRow.children)) {
		const cleanedChildren = newRow.children.map(cleanChildren).filter(Boolean);

		if (cleanedChildren.length > 0) {
			newRow.children = cleanedChildren;
		} else {
			delete newRow.children;
		}
	}

	return newRow;
}

export function pruneEmptyChildren(rows: Row[]): Row[] {
	return rows.map((row) => {
		const hasValidChildren = Array.isArray(row.children) && row.children.length > 0;

		const cleanedRow: Row = {
			...row,
			...(hasValidChildren
				? {
						children: pruneEmptyChildren(row.children!)
					}
				: { children: undefined })
		};

		return cleanedRow;
	});
}

export function findRow(rows: RowComponent[], id: number): RowComponent | undefined {
	for (const row of rows) {
		if (row.getData().id === id) return row;
		const children = row.getTreeChildren();
		if (children.length > 0) {
			const found = findRow(children, id);
			if (found) return found;
		}
	}
	return undefined;
}

export function getAllRows(rows: RowComponent[]): RowComponent[] {
	try {
		let allRows: RowComponent[] = [];

		for (const row of rows) {
			allRows.push(row);
			const children = row.getTreeChildren();
			if (children.length > 0) {
				allRows = allRows.concat(getAllRows(children));
			}
		}

		return allRows;
	} catch (_e) {
		return [];
	}
}

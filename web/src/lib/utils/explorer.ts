/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { FileNode } from '$lib/types/system/file-explorer';

export type SortBy =
	| 'name-asc'
	| 'name-desc'
	| 'modified-asc'
	| 'modified-desc'
	| 'size-desc'
	| 'type';

export interface BreadcrumbItem {
	name: string;
	path: string;
	isLast: boolean;
}

export function generateBreadcrumbItems(currentPath: string): BreadcrumbItem[] {
	const parts = currentPath.split('/').filter(Boolean);
	const items: BreadcrumbItem[] = [];

	items.push({ name: 'My Files', path: '/', isLast: parts.length === 0 });

	let currentBreadcrumbPath = '';
	for (let i = 0; i < parts.length; i++) {
		currentBreadcrumbPath += '/' + parts[i];
		items.push({
			name: parts[i],
			path: currentBreadcrumbPath,
			isLast: i === parts.length - 1
		});
	}
	return items;
}

export function sortFileItems(items: FileNode[], sortBy: SortBy): FileNode[] {
	const sortedItems = [...items];

	switch (sortBy) {
		case 'name-asc':
			return sortedItems.sort((a, b) => {
				const nameA = (a.id.split('/').pop() || a.id).toLowerCase();
				const nameB = (b.id.split('/').pop() || b.id).toLowerCase();
				return nameA.localeCompare(nameB);
			});
		case 'name-desc':
			return sortedItems.sort((a, b) => {
				const nameA = (a.id.split('/').pop() || a.id).toLowerCase();
				const nameB = (b.id.split('/').pop() || b.id).toLowerCase();
				return nameB.localeCompare(nameA);
			});
		case 'modified-asc':
			return sortedItems.sort((a, b) => a.date.getTime() - b.date.getTime());
		case 'modified-desc':
			return sortedItems.sort((a, b) => b.date.getTime() - a.date.getTime());
		case 'size-desc':
			return sortedItems.sort((a, b) => {
				const sizeA = a.size || 0;
				const sizeB = b.size || 0;
				return sizeB - sizeA;
			});
		case 'type':
			return sortedItems.sort((a, b) => {
				if (a.type !== b.type) {
					return a.type === 'folder' ? -1 : 1;
				}
				const nameA = (a.id.split('/').pop() || a.id).toLowerCase();
				const nameB = (b.id.split('/').pop() || b.id).toLowerCase();
				return nameA.localeCompare(nameB);
			});
		default:
			return sortedItems;
	}
}

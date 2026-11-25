/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { Need } from '$lib/stores/permissions.svelte';

export interface NavigationItem {
	id: string;
	label: string;
	icon?: string;
	href?: string;
	need?: Need;
	children?: NavigationItem[];
	defaultOpen?: boolean;
}

export function isCurrent(item: NavigationItem, pathname: string): boolean {
	return Boolean(item.href && (pathname === item.href || pathname.startsWith(`${item.href}/`)));
}

export function containsCurrent(item: NavigationItem, pathname: string): boolean {
	return (
		isCurrent(item, pathname) ||
		Boolean(item.children?.some((child) => containsCurrent(child, pathname)))
	);
}

export function activeAncestors(items: NavigationItem[], pathname: string): string[] {
	return items.flatMap((item) =>
		item.children && containsCurrent(item, pathname)
			? [item.id, ...activeAncestors(item.children, pathname)]
			: []
	);
}

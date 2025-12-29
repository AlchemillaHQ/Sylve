/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

export function deepDiff(
	obj1: Record<string, any>,
	obj2: Record<string, any>,
	path = ''
): { path: string; from: any; to: any }[] {
	const changes = [];

	for (const key of new Set([...Object.keys(obj1 || {}), ...Object.keys(obj2 || {})])) {
		const fullPath = path ? `${path}.${key}` : key;
		const val1 = obj1?.[key];
		const val2 = obj2?.[key];

		if (typeof val1 === 'object' && typeof val2 === 'object' && val1 && val2) {
			changes.push(...deepDiff(val1, val2, fullPath));
		} else if (val1 !== val2) {
			changes.push({ path: fullPath, from: val1, to: val2 });
		}
	}

	return changes;
}

export function findValue(value: unknown, object: Record<string, any>): boolean {
	if (typeof value === 'object' && value !== null) {
		return Object.keys(object).some((key) => object[key] === value);
	}
	return Object.values(object).some((val) => val === value);
}

export function countKeys(obj: Record<string, any>): number {
	return Object.keys(obj).length;
}

/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

export function deepSearchKey(obj: unknown, targetKey: string): unknown[] {
	const results: unknown[] = [];

	function search(current: unknown): void {
		if (Array.isArray(current)) {
			for (const item of current) {
				search(item);
			}
		} else if (typeof current === 'object' && current !== null) {
			const record = current as Record<string, unknown>;

			for (const key in record) {
				if (key === targetKey) {
					results.push(record[key]);
				}

				search(record[key]);
			}
		}
	}

	search(obj);
	return results;
}

export function sameElements<T>(arr1: T[], arr2: T[]): boolean {
	if (arr1.length !== arr2.length) return false;
	const sortedArr1 = [...arr1].sort();
	const sortedArr2 = [...arr2].sort();

	for (let i = 0; i < sortedArr1.length; i++) {
		if (sortedArr1[i] !== sortedArr2[i]) {
			return false;
		}
	}

	return true;
}

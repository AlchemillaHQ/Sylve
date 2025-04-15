/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { deepDiff } from './obj';

export function createEmptyArrayOfArrays(length: number): Array<Array<any>> {
	return Array.from({ length }, () => []);
}

export function deepDiffArr(arr1: any[], arr2: any[]): any[] {
	const changes = [];

	for (let i = 0; i < Math.max(arr1.length, arr2.length); i++) {
		const val1 = arr1[i];
		const val2 = arr2[i];

		if (typeof val1 === 'object' && typeof val2 === 'object' && val1 && val2) {
			changes.push(...deepDiff(val1, val2, `${i}`));
		} else if (val1 !== val2) {
			changes.push({ path: `${i}`, from: val1, to: val2 });
		}
	}

	return changes;
}

export function findValueInArrayByKey<T extends Record<string, any>>(
	value: unknown,
	array: T[],
	key: keyof T
): boolean {
	if (typeof value === 'object' && value !== null) {
		return array.some((obj) => obj[key] === value);
	}
	return array.some((obj) => obj[key] === value);
}

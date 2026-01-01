/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { browser } from '$app/environment';
import { getDB } from '$lib';

export type KVEntry<T = unknown> = {
	key: string;
	value: T;
	timestamp: number;
};

type StoredValue<T = unknown> = {
	data: T;
	timestamp: number;
};

export const kvStorage = {
	async getItem<T>(key: string): Promise<StoredValue<T> | null> {
		if (!browser) return null;

		const db = getDB();
		const entry = await db.kv.get(key);
		return entry ? { data: entry.value as T, timestamp: entry.timestamp } : null;
	},

	async setItem<T>(key: string, data: T, timestamp = Date.now()): Promise<void> {
		if (!browser) return;

		const db = getDB();
		await db.kv.put({ key, value: data, timestamp });
	},

	async removeItem(key: string): Promise<void> {
		if (!browser) return;

		const db = getDB();
		await db.kv.delete(key);
	}
};

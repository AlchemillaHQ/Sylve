/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { Locales } from './types/common';
import type { KVEntry } from './types/db';
import Dexie, { type Table } from 'dexie';
import { createReactiveStorage } from './utils/storage';
import type { AvailableService } from './types/system/settings';

type SharedStorage = {
	token: string | null;
	oldToken: string | null;
	language: Locales | null;
	hostname: string | null;
	nodeId: string | null;
	clusterToken: string | null;
	fileExplorerCurrentPath: string | null;
	visible: boolean | null;
	idle: boolean | null;
	enabledServices: AvailableService[] | null;
};

export const storage = createReactiveStorage<SharedStorage>(
	[
		['token', { storage: 'local' }],
		['oldToken', { storage: 'local' }],
		['language', { storage: 'local' }],
		['hostname', { storage: 'local' }],
		['nodeId', { storage: 'local' }],
		['clusterToken', { storage: 'local' }],
		['fileExplorerCurrentPath', { storage: 'local' }],
		['visible', { storage: 'local' }],
		['idle', { storage: 'local' }],
		['enabledServices', { storage: 'local' }]
	],
	{
		prefix: 'sylve:',
		storage: 'local'
	}
);

export const languageArr: { value: Locales; label: string }[] = [
	{ value: 'en', label: 'English' },
	{ value: 'mal', label: 'മലയാളം' },
	{ value: 'hi', label: 'हिन्दी' }
];

class SylveDB extends Dexie {
	kv!: Table<KVEntry, string>;
	constructor() {
		super('sylve-db');
		this.version(1).stores({ kv: '&key, timestamp' });
	}
}

let _db: SylveDB | null = null;

export function getDB(): SylveDB {
	if (!_db) {
		_db = new SylveDB();
	}

	return _db;
}

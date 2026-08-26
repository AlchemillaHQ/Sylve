/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { ResourceTreeView } from '$lib/resource-tree';

const STORAGE_KEY = 'left-panel-state';
const OPEN_IDS_STORAGE_KEY = 'left-panel-open-ids';
const LEGACY_CLUSTER_OPEN_IDS_STORAGE_KEY = 'clusterIds';

export type ResourceTreeMode = 'single' | 'cluster';

function scopedOpenIdsStorageKey(mode: ResourceTreeMode, view: ResourceTreeView): string {
	return `${OPEN_IDS_STORAGE_KEY}:${mode}:${view}`;
}

function legacyOpenIdsStorageKey(mode: ResourceTreeMode): string {
	return mode === 'cluster' ? LEGACY_CLUSTER_OPEN_IDS_STORAGE_KEY : OPEN_IDS_STORAGE_KEY;
}

function parseOpenIds(value: string, storageKey: string): Set<string> {
	try {
		const parsed: unknown = JSON.parse(value);
		if (!Array.isArray(parsed) || !parsed.every((id) => typeof id === 'string')) {
			throw new TypeError('Expected an array of tree item IDs');
		}
		return new Set(parsed);
	} catch (error) {
		console.error(`Failed to parse open tree IDs from "${storageKey}":`, error);
		localStorage.removeItem(storageKey);
		return new Set<string>();
	}
}

export function saveOpenCategories(state: { [key: string]: boolean }) {
	if (typeof localStorage !== 'undefined') {
		localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
	}
}

export function loadOpenCategories(): { [key: string]: boolean } {
	if (typeof localStorage !== 'undefined') {
		const saved = localStorage.getItem(STORAGE_KEY);
		if (saved) {
			try {
				return JSON.parse(saved);
			} catch (e) {
				console.error('Failed to parse open categories:', e);
				localStorage.removeItem(STORAGE_KEY);
			}
		}
	}
	return {};
}

export function saveOpenIds(
	ids: Set<string>,
	mode: ResourceTreeMode = 'single',
	view: ResourceTreeView = 'server'
) {
	if (typeof localStorage !== 'undefined') {
		localStorage.setItem(scopedOpenIdsStorageKey(mode, view), JSON.stringify(Array.from(ids)));
	}
}

export function hasSavedOpenIds(
	mode: ResourceTreeMode = 'single',
	view: ResourceTreeView = 'server'
): boolean {
	if (typeof localStorage === 'undefined') {
		return false;
	}

	if (localStorage.getItem(scopedOpenIdsStorageKey(mode, view)) !== null) {
		return true;
	}

	return view === 'server' && localStorage.getItem(legacyOpenIdsStorageKey(mode)) !== null;
}

export function loadOpenIds(
	mode: ResourceTreeMode = 'single',
	view: ResourceTreeView = 'server'
): Set<string> {
	if (typeof localStorage !== 'undefined') {
		const scopedKey = scopedOpenIdsStorageKey(mode, view);
		const saved = localStorage.getItem(scopedKey);
		if (saved) {
			return parseOpenIds(saved, scopedKey);
		}

		if (view === 'server') {
			const legacyKey = legacyOpenIdsStorageKey(mode);
			const legacy = localStorage.getItem(legacyKey);
			if (legacy) {
				return parseOpenIds(legacy, legacyKey);
			}
		}
	}
	return new Set<string>();
}

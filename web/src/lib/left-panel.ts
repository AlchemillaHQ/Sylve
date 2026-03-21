/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

const STORAGE_KEY = 'left-panel-state';
const OPEN_IDS_STORAGE_KEY = 'left-panel-open-ids';

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

export function saveOpenIds(ids: Set<string>) {
	if (typeof localStorage !== 'undefined') {
		localStorage.setItem(OPEN_IDS_STORAGE_KEY, JSON.stringify(Array.from(ids)));
	}
}

export function hasSavedOpenIds(): boolean {
	if (typeof localStorage === 'undefined') {
		return false;
	}

	return localStorage.getItem(OPEN_IDS_STORAGE_KEY) !== null;
}

export function loadOpenIds(): Set<string> {
	if (typeof localStorage !== 'undefined') {
		const saved = localStorage.getItem(OPEN_IDS_STORAGE_KEY);
		if (saved) {
			try {
				return new Set(JSON.parse(saved));
			} catch (e) {
				console.error('Failed to parse open ids:', e);
				localStorage.removeItem(OPEN_IDS_STORAGE_KEY);
			}
		}
	}
	return new Set<string>();
}

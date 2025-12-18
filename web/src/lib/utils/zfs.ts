/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { Zpool } from '$lib/types/zfs/pool';

export function isDeviceVdev(device: string, pools: Zpool[]): boolean {
	if (pools.length === 0) {
		return false;
	}

	for (const pool of pools) {
		for (const vdev of pool.vdevs) {
			if (vdev.name === device || vdev.name === `/dev/${device}`) {
				return true;
			}
		}
	}

	return false;
}

export function getHealthHelpers(health: string): { icon: string; color: string; text: string } {
	switch (health) {
		case 'ONLINE':
			return {
				icon: 'carbon:checkmark-filled',
				color: 'text-green-600 dark:text-green-500',
				text: 'Online'
			};
		case 'DEGRADED':
			return {
				icon: 'carbon:warning-filled',
				color: 'text-yellow-600 dark:text-yellow-500',
				text: 'Degraded'
			};
		case 'FAULTED':
			return {
				icon: 'carbon:close-filled',
				color: 'text-red-600 dark:text-red-500',
				text: 'Faulted'
			};
		case 'OFFLINE':
			return {
				icon: 'material-symbols:offline-pin-off',
				color: 'text-red-600 dark:text-red-500',
				text: 'Offline'
			};
		case 'UNAVAIL':
			return {
				icon: 'carbon:warning-alt-filled',
				color: 'text-yellow-600 dark:text-yellow-500',
				text: 'Unavailable'
			};
		case 'REMOVED':
			return {
				icon: 'carbon:warning-alt-filled',
				color: 'text-yellow-600 dark:text-yellow-500',
				text: 'Removed'
			};
		default:
			return {
				icon: 'carbon:warning-alt-filled',
				color: 'text-yellow-600 dark:text-yellow-500',
				text: 'Unknown'
			};
	}
}

export function isValidPoolName(name: string): boolean {
	const reserved = ['log', 'mirror', 'raidz', 'raidz1', 'raidz2', 'raidz3', 'spare'];

	if (!name) return false;
	if (reserved.some((r) => name.startsWith(r))) return false;
	if (!/^[a-zA-Z]/.test(name)) return false;
	if (!/^[a-zA-Z0-9_.-]+$/.test(name)) return false;
	if (name.includes('%')) return false;
	if (/^c[0-9]/.test(name)) return false;

	return true;
}

export function isValidDatasetName(name: string): boolean {
	if (!name || typeof name !== 'string') return false;
	if (name.length > 255) return false;
	if (/[^\x21-\x7E]/.test(name)) return false;
	if (name.includes('%') || name.includes(' ')) return false;

	const components = name.split('/');
	for (const comp of components) {
		if (!comp) return false;
		if (!/^[a-zA-Z0-9_.-]+$/.test(comp)) return false;
		if (comp.startsWith('.') || comp.startsWith('-')) return false;
	}

	return true;
}

export function roundUpToBlock(size: number, block: number): number {
	return Math.ceil(size / block) * block;
}

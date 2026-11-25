/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import {
	aggregatePoolHistory,
	summarizeVerification,
	type Tone
} from '$lib/components/custom/ZFS/Dashboard/dashboard';
import type {
	ZFSDashboardHistory,
	ZFSDashboardPoolSnapshot,
	ZFSDashboardSnapshot
} from '$lib/types/zfs/pool';

export interface DashboardData {
	hostname: string;
	snapshot: ZFSDashboardSnapshot;
	history: ZFSDashboardHistory;
}

export const toneText: Record<Tone, string> = {
	neutral: 'text-muted-foreground',
	success: 'text-green-600 dark:text-green-500',
	warning: 'text-amber-600 dark:text-amber-400',
	danger: 'text-red-600 dark:text-red-400'
};

export const toneMeter: Record<Tone, string> = {
	neutral: 'bg-zen-accent',
	success: 'bg-zen-accent',
	warning: 'bg-amber-500',
	danger: 'bg-red-600 dark:bg-red-500'
};

export function poolErrors(pool: ZFSDashboardPoolSnapshot): number {
	return pool.errors.read + pool.errors.write + pool.errors.checksum + pool.errors.scan;
}

export function usedPercent(pool: ZFSDashboardPoolSnapshot): number {
	return pool.size > 0 ? (pool.allocated / pool.size) * 100 : 0;
}

export function verificationSummary(pools: ZFSDashboardPoolSnapshot[]) {
	const normalized = pools.map((pool) =>
		pool.scan?.function.toUpperCase() === 'NONE' || pool.scan?.state.toUpperCase() === 'NONE'
			? { ...pool, scan: null }
			: pool
	);
	const summary = summarizeVerification(normalized);
	if (
		summary.tone === 'success' &&
		normalized.some((pool) => pool.scan?.state.toUpperCase() !== 'FINISHED')
	) {
		return {
			value: 'Verification unknown',
			detail: 'A completed scrub or resilver could not be confirmed',
			footer: 'Review the reported scan state',
			tone: 'warning' as const,
			progress: null
		};
	}
	return summary;
}

export function scopedHistory(history: ZFSDashboardHistory, pools: ZFSDashboardPoolSnapshot[]) {
	const guids = new Set(pools.map((pool) => pool.guid));
	return aggregatePoolHistory(history.pools.filter((series) => guids.has(series.guid)));
}

export function latency(
	pools: ZFSDashboardPoolSnapshot[],
	direction: 'read' | 'write'
): number | null {
	let weighted = 0;
	let operations = 0;
	for (const { io } of pools) {
		const value = direction === 'read' ? io.readLatencyNanos : io.writeLatencyNanos;
		const count = direction === 'read' ? io.readIOPS : io.writeIOPS;
		if (!io.valid || !io.latencyAvailable || value === null || count <= 0) continue;
		weighted += value * count;
		operations += count;
	}
	return operations > 0 ? weighted / operations / 1_000_000 : null;
}

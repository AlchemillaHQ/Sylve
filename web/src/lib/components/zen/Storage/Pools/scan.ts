/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { Tone } from '$lib/components/custom/ZFS/Dashboard/dashboard';
import type { ZpoolStatusPool } from '$lib/types/zfs/pool';
import { formatBytesBinary, parseSizeInputToBytes } from '$lib/utils/bytes';
import { convertDbTime } from '$lib/utils/time';
import { isPoolScanActive } from '$lib/utils/zfs/pool';
import { reportedNumber } from './pools';

export const scrubSchedulesCacheKey = 'zen-pool-scrub-schedules';
export const scrubRunsCacheKey = (guid: string) => `zen-pool-scrub-runs-${guid}`;

export function scrubLabel(value: string): string {
	return value.replaceAll('_', ' ').replace(/\b\w/g, (letter) => letter.toUpperCase()) || 'Unknown';
}

export function scrubTone(value: string): Tone {
	if (value === 'passed') return 'success';
	if (value === 'failed') return 'danger';
	if (['paused', 'cancelled', 'unknown', 'missed'].includes(value)) return 'warning';
	return 'neutral';
}

export const scanIconColor: Record<Tone, string> = {
	neutral: 'text-zen-highlight-foreground',
	success: 'text-green-600 dark:text-green-400',
	warning: 'text-amber-700 dark:text-amber-400',
	danger: 'text-red-700 dark:text-red-400'
};

export const scrubDot: Record<Tone, string> = {
	neutral: 'bg-muted-foreground',
	success: 'bg-green-600 dark:bg-green-400',
	warning: 'bg-amber-600 dark:bg-amber-400',
	danger: 'bg-red-600 dark:bg-red-400'
};

export function scanTime(value: string | number | null | undefined): number | null {
	if (value === null || value === undefined || value === '' || value === '-') return null;
	const number = Number(value);
	const time = Number.isFinite(number)
		? number > 1e12
			? number
			: number * 1000
		: Date.parse(String(value));
	return Number.isFinite(time) && time > 0 && time <= 8.64e15 ? time : null;
}

export function scanDate(value: string | number | null | undefined, fallback = '—'): string {
	const time = scanTime(value);
	return time === null ? fallback : convertDbTime(new Date(time).toISOString());
}

export function scanDuration(start: number | null, end: number | null): string {
	if (
		start === null ||
		end === null ||
		!Number.isFinite(start) ||
		!Number.isFinite(end) ||
		end < start
	)
		return '—';
	const seconds = Math.floor((end - start) / 1000);
	if (seconds >= 86400)
		return `${Math.floor(seconds / 86400)}d ${Math.floor((seconds % 86400) / 3600)}h`;
	if (seconds >= 3600)
		return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
	if (seconds >= 60) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
	return `${seconds}s`;
}

export function scanBytes(value: unknown): string {
	return formatBytesBinary(value, { fallback: '—' });
}

export function describeScan(stats: ZpoolStatusPool['scan_stats']) {
	const kind = /resilver/i.test(stats?.function ?? '')
		? 'Resilver'
		: /scrub/i.test(stats?.function ?? '')
			? 'Scrub'
			: 'Scan';
	const active = isPoolScanActive(stats);
	const known = active || Boolean(stats?.function && stats.function.toUpperCase() !== 'NONE');
	const paused =
		active && (stats?.state?.toUpperCase() === 'PAUSED' || scanTime(stats?.scrub_pause) !== null);
	const finished = known && stats?.state?.toUpperCase() === 'FINISHED';
	const cancelled = known && ['CANCELED', 'CANCELLED'].includes(stats?.state?.toUpperCase() ?? '');
	const errors = reportedNumber(stats?.errors);
	const start = scanTime(stats?.start_time);
	const end = scanTime(stats?.end_time);
	const total = parseSizeInputToBytes(stats?.to_examine);
	const completed = parseSizeInputToBytes(
		kind === 'Resilver' ? stats?.processed : (stats?.issued ?? stats?.examined)
	);
	const progress =
		active && completed !== null && total !== null && total > 0
			? Math.min(100, Math.max(0, (completed / total) * 100))
			: null;
	const phase = active
		? paused
			? 'Paused'
			: 'Running'
		: finished
			? errors === 0
				? 'Passed'
				: errors !== null && errors > 0
					? 'Errors found'
					: 'Completed'
			: cancelled
				? 'Cancelled'
				: known
					? scrubLabel(stats?.state ?? '')
					: 'Not Reported';
	const tone: Tone =
		errors !== null && errors > 0
			? 'danger'
			: paused || cancelled
				? 'warning'
				: finished && errors === 0
					? 'success'
					: 'neutral';
	const elapsed = start === null ? null : (Date.now() - start) / 1000;
	const rate =
		active && !paused && completed !== null && elapsed !== null && elapsed > 0
			? completed / elapsed
			: null;
	const remaining =
		rate !== null && rate > 0 && total !== null && completed !== null
			? Math.max(0, (total - completed) / rate)
			: null;
	return {
		kind,
		known,
		active,
		paused,
		finished,
		errors,
		start,
		end,
		progress,
		phase,
		tone,
		rate,
		remaining
	};
}

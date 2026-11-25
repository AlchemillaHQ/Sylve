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
	SmartSelfTestDetailsSchema,
	SmartSelfTestScheduleSchema,
	type SmartSelfTestCapabilities,
	type SmartSelfTestDetails,
	type SmartSelfTestResult,
	type SmartSelfTestSchedule,
	type SmartSelfTestScheduleType,
	type SmartSelfTestStartKind
} from '$lib/types/disk/disk';
import type { Tone } from '$lib/components/custom/ZFS/Dashboard/dashboard';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, updateCache } from '$lib/utils/http';
import { z } from 'zod/v4';

const SnapshotSchema = z.object({
	details: SmartSelfTestDetailsSchema,
	schedules: z.array(SmartSelfTestScheduleSchema),
	readAt: z.number()
});
export type SelfTestSnapshot = z.infer<typeof SnapshotSchema>;

const snapshots = new Map<string, SelfTestSnapshot>();

function snapshotKey(device: string, uuid: string): string {
	return `disk-self-test:${uuid}:${device}`;
}

export function memorySnapshot(
	hostname: string,
	device: string,
	uuid: string
): SelfTestSnapshot | undefined {
	return snapshots.get(`${hostname}\n${snapshotKey(device, uuid)}`);
}

export async function storedSnapshot(
	hostname: string,
	device: string,
	uuid: string
): Promise<SelfTestSnapshot | null> {
	try {
		return await cachedFetch<SelfTestSnapshot>(
			snapshotKey(device, uuid),
			async () => {
				throw new Error('unreachable');
			},
			SEVEN_DAYS,
			true,
			hostname,
			SnapshotSchema
		);
	} catch {
		return null;
	}
}

export function saveSnapshot(
	hostname: string,
	device: string,
	uuid: string,
	snapshot: SelfTestSnapshot
) {
	snapshots.set(`${hostname}\n${snapshotKey(device, uuid)}`, snapshot);
	void updateCache(snapshotKey(device, uuid), snapshot, hostname);
}

export const testKinds: { kind: SmartSelfTestStartKind; label: string; description: string }[] = [
	{ kind: 'short', label: 'Short', description: 'A quick check of the drive, for routine use.' },
	{
		kind: 'extended',
		label: 'Extended',
		description: 'A full scan of the drive, for a thorough check.'
	},
	{
		kind: 'conveyance',
		label: 'Conveyance',
		description: 'Looks for damage from shipping or moving the drive.'
	}
];

export function testLabel(kind: string): string {
	return (
		testKinds.find((test) => test.kind === kind)?.label ??
		formatLabel(kind.replace(/_captive$/, ''))
	);
}

export function durationMinutes(kind: string, capabilities?: SmartSelfTestCapabilities): number {
	if (!capabilities) return 0;
	switch (kind) {
		case 'short':
			return capabilities.short_duration_minutes;
		case 'extended':
			return capabilities.extended_duration_minutes;
		case 'conveyance':
			return capabilities.conveyance_duration_minutes;
		default:
			return 0;
	}
}

export function formatDuration(minutes: number): string {
	if (minutes <= 0) return 'Duration not reported';
	if (minutes < 60) return `About ${minutes} min`;
	const hours = Math.floor(minutes / 60);
	const rest = minutes % 60;
	return rest === 0 ? `About ${hours} h` : `About ${hours} h ${rest} min`;
}

export function formatLabel(value: string): string {
	if (!value.trim()) return 'Unknown';
	return value
		.trim()
		.replaceAll('_', ' ')
		.replace(/\b\w/g, (character) => character.toUpperCase());
}

export function scheduleActive(schedule: SmartSelfTestSchedule): boolean {
	return schedule.lastStatus === 'starting' || schedule.lastStatus === 'running';
}

export const schedulableKinds: SmartSelfTestScheduleType[] = ['short', 'extended'];

export const schedulePresets: Record<
	SmartSelfTestScheduleType,
	{ label: string; cronExpr: string }[]
> = {
	short: [
		{ label: 'Daily · 02:00', cronExpr: '0 2 * * *' },
		{ label: 'Weekly · Sunday 02:00', cronExpr: '0 2 * * 0' },
		{ label: 'Monthly · Day 1, 02:00', cronExpr: '0 2 1 * *' }
	],
	extended: [
		{ label: 'Weekly · Sunday 03:00', cronExpr: '0 3 * * 0' },
		{ label: 'Monthly · Day 1, 03:00', cronExpr: '0 3 1 * *' }
	]
};

export function scheduleTone(status: string): Tone {
	if (status === 'passed') return 'success';
	if (status === 'failed') return 'danger';
	if (['aborted', 'unknown', 'missed'].includes(status)) return 'warning';
	return 'neutral';
}

export function scheduleStatusLabel(schedule: SmartSelfTestSchedule): string {
	if (schedule.lastStatus === 'idle') return schedule.lastRunAt ? 'Idle' : 'Not Run Yet';
	return formatLabel(schedule.lastStatus);
}

export function progressPercent(
	details: SmartSelfTestDetails | null,
	schedule?: SmartSelfTestSchedule
): number | null {
	const status = details?.status;
	const clamp = (value: number) => Math.min(100, Math.max(0, value));
	if (status?.running) {
		if (status.progress_known && status.progress_pct >= 0) return clamp(status.progress_pct);
		if (status.remaining_known && status.remaining_pct >= 0)
			return clamp(100 - status.remaining_pct);
	}
	if (schedule?.progressKnown && schedule.progressPct >= 0) return clamp(schedule.progressPct);
	return null;
}

export type ResultTone = 'passed' | 'failed' | 'aborted' | 'running' | 'unknown';

export function resultTone(result: SmartSelfTestResult): ResultTone {
	const outcome = result.outcome.toLowerCase();
	const status = result.status.toLowerCase();
	if (
		outcome === 'failed' ||
		status.startsWith('failed') ||
		['fatal', 'unknown_error', 'completed_segment_failed'].includes(status)
	) {
		return 'failed';
	}
	if (
		outcome === 'aborted' ||
		status.startsWith('aborted') ||
		['interrupted', 'cancelled'].includes(status)
	) {
		return 'aborted';
	}
	if (outcome === 'in_progress' || status === 'in_progress' || status === 'running')
		return 'running';
	if (outcome === 'passed' || status === 'completed') return 'passed';
	return 'unknown';
}

export function resultOutcome(result: SmartSelfTestResult): string {
	return formatLabel(result.outcome || result.status);
}

export function resultHours(result: SmartSelfTestResult): string | null {
	const hours = result.lifetime_hours_exact.trim() || result.lifetime_hours.toString();
	return resultTone(result) === 'running' && Number(hours) === 0 ? null : hours;
}

export function resultDate(result: SmartSelfTestResult): number {
	const timestamp = result.started_at ? Date.parse(result.started_at) : NaN;
	return Number.isFinite(timestamp) ? timestamp : -1;
}

export function resultLocation(result: SmartSelfTestResult): string {
	const values: string[] = [];
	if (result.nsid_valid) values.push(`NSID ${result.nsid}`);
	if (result.lba_valid) values.push(`LBA ${result.lba_exact.trim() || result.lba.toString()}`);
	if (values.length > 0) return values.join(' · ');
	const tone = resultTone(result);
	if (tone === 'running') return '—';
	return tone === 'passed' ? 'None' : 'Not Reported';
}

export function resultDetail(result: SmartSelfTestResult): string {
	const values: string[] = [];
	const running = resultTone(result) === 'running';
	const hex = (value: number) => value.toString(16).padStart(2, '0');
	if (result.status) values.push(formatLabel(result.status));
	if (result.segment_num > 0) values.push(`Segment ${result.segment_num}`);
	if (running && result.remaining_pct === 0) values.push('Less than 10% remaining');
	if (running && result.remaining_pct > 0) values.push(`${result.remaining_pct}% remaining`);
	if (!running && result.remaining_pct > 0)
		values.push(`${result.remaining_pct}% remaining when stopped`);
	if (result.sense_key !== 0 || result.asc !== 0 || result.ascq !== 0) {
		values.push(`Sense ${hex(result.sense_key)}/${hex(result.asc)}/${hex(result.ascq)}`);
	}
	if (result.status_code_type_valid) values.push(`Status type ${result.status_code_type}`);
	if (result.status_code_valid) values.push(`Status code ${result.status_code}`);
	if (result.checkpoint > 0) values.push(`Checkpoint ${result.checkpoint}`);
	if (result.parameter_code !== 0) values.push(`Parameter 0x${result.parameter_code.toString(16)}`);
	if (result.vendor_specific !== 0) values.push(`Vendor 0x${result.vendor_specific.toString(16)}`);
	return values.join(' · ') || '—';
}

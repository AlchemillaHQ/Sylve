/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { SnapshotScheduleSettings } from '$lib/api/zfs/datasets';
import type { Dataset, PeriodicSnapshot } from '$lib/types/zfs/dataset';
import { cronToHuman } from '$lib/utils/time';
import {
	presetToSchedule,
	scheduleToPreset,
	snapshotScheduleLabel,
	snapshotSchedulePresets
} from '$lib/utils/zfs/snapshot';
import type { PickedDataset } from '../FileSystems/filesystems';

export const CRON = 'cron';

export type RetentionMode = 'all' | 'simple' | 'gfs';

export type CountKey =
	| 'keepLast'
	| 'maxAgeDays'
	| 'keepHourly'
	| 'keepDaily'
	| 'keepWeekly'
	| 'keepMonthly'
	| 'keepYearly';

export interface ScheduleDraft {
	dataset: PickedDataset | undefined;
	prefix: string;
	recursive: boolean;
	frequency: string;
	cron: string;
	retention: RetentionMode;
	simple: Record<'keepLast' | 'maxAgeDays', string>;
	gfs: Record<Exclude<CountKey, 'maxAgeDays'>, string>;
}

export const retentionChoices: { value: RetentionMode; label: string }[] = [
	{ value: 'simple', label: 'Keep Recent' },
	{ value: 'gfs', label: 'Thin Out Over Time' },
	{ value: 'all', label: 'Keep All' }
];

export const simpleFields: { key: 'keepLast' | 'maxAgeDays'; label: string }[] = [
	{ key: 'keepLast', label: 'Keep Last' },
	{ key: 'maxAgeDays', label: 'Keep Days' }
];

export const gfsFields: { key: Exclude<CountKey, 'maxAgeDays'>; label: string }[] = [
	{ key: 'keepLast', label: 'Keep Last' },
	{ key: 'keepHourly', label: 'Hourly' },
	{ key: 'keepDaily', label: 'Daily' },
	{ key: 'keepWeekly', label: 'Weekly' },
	{ key: 'keepMonthly', label: 'Monthly' },
	{ key: 'keepYearly', label: 'Yearly' }
];

const defaultSimple = { keepLast: '24', maxAgeDays: '0' };
const defaultGFS = {
	keepLast: '20',
	keepHourly: '24',
	keepDaily: '7',
	keepWeekly: '4',
	keepMonthly: '12',
	keepYearly: '3'
};

export function newScheduleDraft(): ScheduleDraft {
	return {
		dataset: undefined,
		prefix: 'auto',
		recursive: false,
		frequency: '3600',
		cron: '',
		retention: 'simple',
		simple: { ...defaultSimple },
		gfs: { ...defaultGFS }
	};
}

function count(value: number | undefined): string {
	return String(value ?? 0);
}

export function editScheduleDraft(job: PeriodicSnapshot, datasetName: string): ScheduleDraft {
	const gfsSet = [job.keepHourly, job.keepDaily, job.keepWeekly, job.keepMonthly, job.keepYearly]
		.map((value) => value ?? 0)
		.some((value) => value > 0);
	const simpleSet = (job.keepLast ?? 0) > 0 || (job.maxAgeDays ?? 0) > 0;
	const retention: RetentionMode = gfsSet ? 'gfs' : simpleSet ? 'simple' : 'all';
	const cronExpr = job.cronExpr.trim();
	const preset = scheduleToPreset(job.interval, cronExpr);
	return {
		dataset: { name: datasetName, guid: job.guid },
		prefix: job.prefix,
		recursive: job.recursive,
		frequency: preset ?? (cronExpr ? CRON : String(job.interval)),
		cron: preset ? '' : cronExpr,
		retention,
		simple:
			retention === 'simple'
				? { keepLast: count(job.keepLast), maxAgeDays: count(job.maxAgeDays) }
				: { ...defaultSimple },
		gfs:
			retention === 'gfs'
				? {
						keepLast: count(job.keepLast),
						keepHourly: count(job.keepHourly),
						keepDaily: count(job.keepDaily),
						keepWeekly: count(job.keepWeekly),
						keepMonthly: count(job.keepMonthly),
						keepYearly: count(job.keepYearly)
					}
				: { ...defaultGFS }
	};
}

export function frequencyChoices(current: string): { value: string; label: string }[] {
	const choices = [...snapshotSchedulePresets];
	if (current !== CRON && !choices.some((choice) => choice.value === current)) {
		choices.push({ value: current, label: snapshotScheduleLabel(Number(current), '') });
	}
	return [...choices, { value: CRON, label: 'Custom (Cron)' }];
}

const reservedPrefixes = ['ha_', 'bk_', 'sylve-migrate-', 'sylve_create_'];
const exampleSuffix = '-2026-01-01-00-00';

export function prefixProblem(
	prefix: string,
	dataset: Pick<Dataset, 'name' | 'guid'> | undefined,
	jobs: Pick<PeriodicSnapshot, 'guid' | 'prefix'>[]
): string {
	const value = prefix.trim();
	if (!value) return 'Enter a prefix';
	if (!/^[a-zA-Z0-9_.:-]+$/.test(value)) return 'Use letters, digits, and - _ . or : only';
	const lower = `${value.toLowerCase()}-`;
	const reserved = reservedPrefixes.find((entry) => lower.startsWith(entry));
	if (reserved)
		return `Prefixes starting with ${reserved.replace(/-$/, '')} are reserved for Sylve`;
	if (!dataset) return '';
	if (`${dataset.name}@${value}${exampleSuffix}`.length > 255)
		return 'The snapshot names would be longer than 255 characters';
	if (jobs.some((job) => job.guid === dataset.guid && job.prefix === value))
		return 'Another schedule on this file system uses this prefix';
	return '';
}

export function cronProblem(expression: string): string {
	const value = expression.trim();
	if (!value) return 'Enter a cron expression';
	if (!value.startsWith('@') && value.split(/\s+/).length !== 5)
		return 'Use five fields: minute, hour, day, month and weekday';
	if (!cronToHuman(value)) return 'Not a valid cron expression';
	return '';
}

export function countProblem(value: string): string {
	const trimmed = value.trim();
	if (trimmed === '') return '';
	if (!/^\d+$/.test(trimmed)) return 'Use a whole number';
	if (Number(trimmed) > 100000) return 'At most 100000';
	return '';
}

function countValue(value: string): number {
	return Number(value.trim()) || 0;
}

export function retentionProblem(draft: ScheduleDraft): string {
	if (draft.retention === 'all') return '';
	const values = draft.retention === 'simple' ? draft.simple : draft.gfs;
	if (Object.values(values).some((value) => countProblem(value))) return '';
	if (Object.values(values).every((value) => countValue(value) === 0))
		return 'Set at least one value above zero';
	return '';
}

export function scheduleSettings(draft: ScheduleDraft): SnapshotScheduleSettings {
	const schedule =
		draft.frequency === CRON
			? { interval: 0, cronExpr: draft.cron.trim() }
			: presetToSchedule(draft.frequency);
	const zero = {
		keepLast: 0,
		maxAgeDays: 0,
		keepHourly: 0,
		keepDaily: 0,
		keepWeekly: 0,
		keepMonthly: 0,
		keepYearly: 0
	};
	if (draft.retention === 'simple') {
		return {
			...schedule,
			retentionType: 'simple',
			...zero,
			keepLast: countValue(draft.simple.keepLast),
			maxAgeDays: countValue(draft.simple.maxAgeDays)
		};
	}
	if (draft.retention === 'gfs') {
		return {
			...schedule,
			retentionType: 'gfs',
			...zero,
			keepLast: countValue(draft.gfs.keepLast),
			keepHourly: countValue(draft.gfs.keepHourly),
			keepDaily: countValue(draft.gfs.keepDaily),
			keepWeekly: countValue(draft.gfs.keepWeekly),
			keepMonthly: countValue(draft.gfs.keepMonthly),
			keepYearly: countValue(draft.gfs.keepYearly)
		};
	}
	return { ...schedule, retentionType: 'none', ...zero };
}

export function settingsChanged(a: SnapshotScheduleSettings, b: SnapshotScheduleSettings): boolean {
	return (Object.keys(a) as (keyof SnapshotScheduleSettings)[]).some((key) => a[key] !== b[key]);
}

export function scheduleSaveErrorMessage(message: string): string {
	if (/periodic_snapshot_job_\d+_not_found/.test(message))
		return 'This schedule was deleted. Close this dialog and refresh.';
	if (/unique/i.test(message))
		return 'This file system already has a schedule with this prefix and frequency.';
	if (/snapshot_namespace_reserved/.test(message)) return 'This prefix is reserved for Sylve.';
	if (/invalid_schedule/.test(message))
		return 'The schedule isn’t valid. Check the cron expression.';
	if (/dataset_with_guid_not_found|_not_found/.test(message))
		return 'The file system no longer exists.';
	if (/retention/.test(message)) return 'Check the retention settings.';
	return message;
}

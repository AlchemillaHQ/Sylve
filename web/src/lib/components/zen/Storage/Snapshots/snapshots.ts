/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { APIResponse } from '$lib/types/common';
import type { BasicSettings } from '$lib/types/system/settings';
import type {
	Dataset,
	DetailedDataset,
	PeriodicSnapshot,
	SnapshotSummary
} from '$lib/types/zfs/dataset';
import { dateToAgo } from '$lib/utils/time';
import { isSystemMountpoint } from '../FileSystems/actions';
import { isSylveDataset } from '../FileSystems/filesystems';

export interface SnapshotsData {
	hostname: string;
	snapshots: SnapshotSummary[] | APIResponse;
	periodicSnapshots: PeriodicSnapshot[] | APIResponse;
	basicSettings?: BasicSettings;
	fresh?: boolean;
}

const sylveKinds: [prefix: string, kind: string][] = [
	['bk_', 'Backup'],
	['ha_', 'Replication'],
	['sylve-migrate-', 'Migration'],
	['sylve_create_', 'Sylve'],
	['svms_', 'VM'],
	['sjs_', 'Jail'],
	['sylve_template_', 'Template'],
	['sylve_vm_template_', 'Template']
];

export function sylveSnapshotKind(snapshot: Pick<SnapshotSummary, 'snapshot'>): string | null {
	const name = snapshot.snapshot.toLowerCase();
	return sylveKinds.find(([prefix]) => name.startsWith(prefix))?.[1] ?? null;
}

export function isVisibleSnapshot(snapshot: SnapshotSummary): boolean {
	return !isSylveDataset(snapshot.dataset);
}

export function createdTime(snapshot: SnapshotSummary): number {
	const time = snapshot.createdAt.getTime();
	return Number.isFinite(time) && snapshot.createdAt.getUTCFullYear() > 1970 ? time : 0;
}

export type SnapshotCategory = 'yours' | 'scheduled' | 'backup' | 'replication' | 'other';

export const snapshotCategories: { value: SnapshotCategory; label: string }[] = [
	{ value: 'yours', label: 'Yours' },
	{ value: 'scheduled', label: 'Scheduled' },
	{ value: 'backup', label: 'Backups' },
	{ value: 'replication', label: 'Replication' },
	{ value: 'other', label: 'Other Sylve' }
];

export const defaultCategories: SnapshotCategory[] = ['yours', 'scheduled'];

function escapeRegExp(value: string): string {
	return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

export function scheduledNamePattern(prefix: string): RegExp {
	return new RegExp(`^${escapeRegExp(prefix)}-\\d{4}-\\d{2}-\\d{2}-\\d{2}-\\d{2}$`);
}

export function scheduledMatcher(
	jobs: Pick<PeriodicSnapshot, 'prefix' | 'pool'>[]
): (snapshot: SnapshotSummary) => boolean {
	const patterns = jobs.map((job) => ({
		pool: job.pool,
		pattern: scheduledNamePattern(job.prefix)
	}));
	return (snapshot) =>
		patterns.some(({ pool, pattern }) => pool === snapshot.pool && pattern.test(snapshot.snapshot));
}

export function snapshotCategory(
	snapshot: SnapshotSummary,
	isScheduled: (snapshot: SnapshotSummary) => boolean
): SnapshotCategory {
	const kind = sylveSnapshotKind(snapshot);
	if (kind === 'Backup') return 'backup';
	if (kind === 'Replication') return 'replication';
	if (kind) return 'other';
	return isScheduled(snapshot) ? 'scheduled' : 'yours';
}

export type SnapshotIndex = Map<string, SnapshotSummary[]>;

export function indexByName(snapshots: SnapshotSummary[]): SnapshotIndex {
	const index: SnapshotIndex = new Map();
	for (const snapshot of snapshots) {
		const group = index.get(snapshot.snapshot);
		if (group) group.push(snapshot);
		else index.set(snapshot.snapshot, [snapshot]);
	}
	return index;
}

export function sameNamedInside(
	snapshot: SnapshotSummary,
	index: SnapshotIndex
): SnapshotSummary[] {
	const prefix = `${snapshot.dataset}/`;
	return (index.get(snapshot.snapshot) ?? []).filter((other) => other.dataset.startsWith(prefix));
}

export function sameNamedInsideAll(
	chosen: SnapshotSummary[],
	all: SnapshotSummary[]
): SnapshotSummary[] {
	const index = indexByName(all);
	const chosenNames = new Set(chosen.map((snapshot) => snapshot.name));
	const found = new Map<string, SnapshotSummary>();
	for (const snapshot of chosen) {
		for (const other of sameNamedInside(snapshot, index)) {
			if (!chosenNames.has(other.name)) found.set(other.name, other);
		}
	}
	return [...found.values()].sort((a, b) => a.name.localeCompare(b.name));
}

export function countCategories(
	categories: Iterable<SnapshotCategory>
): Map<SnapshotCategory, number> {
	const counts = new Map<SnapshotCategory, number>();
	for (const category of categories) counts.set(category, (counts.get(category) ?? 0) + 1);
	return counts;
}

export function toggledCategories(
	current: SnapshotCategory[],
	category: SnapshotCategory,
	checked: boolean
): SnapshotCategory[] {
	const next = new Set(current);
	if (checked) next.add(category);
	else next.delete(category);
	return snapshotCategories.map((entry) => entry.value).filter((value) => next.has(value));
}

const kindReasons: Record<string, string> = {
	Backup: 'Sylve uses this snapshot for backups. Deleting it would break the backup chain.',
	Replication: 'Sylve uses this snapshot for replication. Deleting it would break replication.',
	Migration: 'Sylve uses this snapshot to migrate a guest.',
	VM: 'This is a virtual machine snapshot. Manage it from the virtual machine.',
	Jail: 'This is a jail snapshot. Manage it from the jail.',
	Template: 'Sylve uses this snapshot as a template.',
	Sylve: 'Sylve uses this snapshot to create guests.'
};

export function snapshotSylveUse(snapshot: SnapshotSummary): string {
	const kind = sylveSnapshotKind(snapshot);
	if (kind) return kindReasons[kind];
	if (!isVisibleSnapshot(snapshot))
		return 'Sylve manages the file system this snapshot belongs to.';
	return '';
}

export function newerSnapshots(target: SnapshotSummary, all: SnapshotSummary[]): SnapshotSummary[] {
	return all
		.filter(
			(snapshot) => snapshot.dataset === target.dataset && snapshot.createTxg > target.createTxg
		)
		.sort((a, b) => a.createTxg - b.createTxg);
}

export function systemMountpoints(datasets: Dataset[]): Map<string, string> {
	return new Map(
		datasets
			.filter((dataset) => isSystemMountpoint(dataset.mountpoint))
			.map((dataset) => [dataset.name, dataset.mountpoint.trim()])
	);
}

export function rollbackBlock(
	target: SnapshotSummary,
	all: SnapshotSummary[],
	system: Map<string, string>
): string {
	if (!isVisibleSnapshot(target)) return 'Sylve manages the file system this snapshot belongs to.';
	const mountpoint = system.get(target.dataset);
	if (mountpoint)
		return `Its mount point ${mountpoint} belongs to the operating system. Use a boot environment to restore it.`;
	if (!target.createTxg) return 'Snapshot details are still loading. Refresh and try again.';
	for (const snapshot of newerSnapshots(target, all)) {
		const reason = snapshotDeleteBlock(snapshot);
		if (reason)
			return `Rolling back would delete the newer snapshot @${snapshot.snapshot}. ${reason}`;
	}
	return '';
}

export function snapshotDeleteBlock(snapshot: SnapshotSummary): string {
	return snapshotSylveUse(snapshot) || snapshotZFSBlock(snapshot);
}

export function snapshotZFSBlock(snapshot: SnapshotSummary): string {
	if (snapshot.clones.length > 0) {
		const others = snapshot.clones.length - 1;
		return `${snapshot.clones[0]}${others > 0 ? ` and ${others} more` : ''} ${others > 0 ? 'were' : 'was'} cloned from this snapshot. Delete or promote ${others > 0 ? 'them' : 'it'} first.`;
	}
	if (snapshot.holds > 0) {
		return `It has ${snapshot.holds} ${snapshot.holds === 1 ? 'hold' : 'holds'}, usually from a backup or transfer in progress.`;
	}
	return '';
}

export function pickableDatasets(datasets: DetailedDataset[]): DetailedDataset[] {
	return datasets
		.filter((dataset) => !isSylveDataset(dataset.name))
		.sort((a, b) => a.name.localeCompare(b.name));
}

export interface ScheduleRow {
	job: PeriodicSnapshot;
	dataset: string;
	snapshots: number;
}

export function scheduleSnapshots(
	row: Pick<ScheduleRow, 'job' | 'dataset'>,
	snapshots: SnapshotSummary[]
): SnapshotSummary[] {
	if (!row.dataset) return [];
	const pattern = scheduledNamePattern(row.job.prefix);
	return snapshots.filter(
		(snapshot) =>
			(snapshot.dataset === row.dataset ||
				(row.job.recursive && snapshot.dataset.startsWith(`${row.dataset}/`))) &&
			pattern.test(snapshot.snapshot)
	);
}

export function scheduleRows(
	jobs: PeriodicSnapshot[],
	datasets: Pick<Dataset, 'name' | 'guid'>[],
	snapshots: SnapshotSummary[]
): ScheduleRow[] {
	const names = new Map(datasets.map((dataset) => [dataset.guid, dataset.name]));
	return jobs.map((job) => {
		const dataset = names.get(job.guid) ?? '';
		return { job, dataset, snapshots: scheduleSnapshots({ job, dataset }, snapshots).length };
	});
}

export interface ScheduleCleanup {
	deletable: SnapshotSummary[];
	shared: SnapshotSummary[];
	blocked: SnapshotSummary[];
}

export function scheduleCleanup(
	targets: ScheduleRow[],
	rows: ScheduleRow[],
	snapshots: SnapshotSummary[]
): ScheduleCleanup {
	const targetIds = new Set(targets.map((row) => row.job.id));
	const candidates = new Map<string, SnapshotSummary>();
	for (const row of targets) {
		for (const snapshot of scheduleSnapshots(row, snapshots))
			candidates.set(snapshot.name, snapshot);
	}
	const claimed = new Set(
		rows
			.filter((row) => !targetIds.has(row.job.id))
			.flatMap((row) => scheduleSnapshots(row, snapshots).map((snapshot) => snapshot.name))
	);
	const cleanup: ScheduleCleanup = { deletable: [], shared: [], blocked: [] };
	for (const snapshot of [...candidates.values()].sort((a, b) => a.name.localeCompare(b.name))) {
		if (claimed.has(snapshot.name)) cleanup.shared.push(snapshot);
		else if (sylveSnapshotKind(snapshot) || snapshotZFSBlock(snapshot))
			cleanup.blocked.push(snapshot);
		else cleanup.deletable.push(snapshot);
	}
	return cleanup;
}

export function chunked<T>(items: T[], size: number): T[][] {
	const chunks: T[][] = [];
	for (let start = 0; start < items.length; start += size)
		chunks.push(items.slice(start, start + size));
	return chunks;
}

function listPhrase(parts: string[]): string {
	if (parts.length <= 1) return parts.join('');
	return `${parts.slice(0, -1).join(', ')} and ${parts.at(-1)}`;
}

const retentionBuckets: [
	key: 'keepHourly' | 'keepDaily' | 'keepWeekly' | 'keepMonthly' | 'keepYearly',
	label: string,
	unit: string
][] = [
	['keepHourly', 'hourly', 'hour'],
	['keepDaily', 'daily', 'day'],
	['keepWeekly', 'weekly', 'week'],
	['keepMonthly', 'monthly', 'month'],
	['keepYearly', 'yearly', 'year']
];

export function retentionSummary(job: PeriodicSnapshot): { label: string; description: string } {
	const labels: string[] = [];
	const phrases: string[] = [];
	const keepLast = job.keepLast ?? 0;
	if (keepLast > 0) {
		labels.push(keepLast === 1 ? 'latest' : `last ${keepLast}`);
		phrases.push(keepLast === 1 ? 'the latest snapshot' : `the last ${keepLast} snapshots`);
	}
	for (const [key, label, unit] of retentionBuckets) {
		const count = job[key] ?? 0;
		if (count > 0) {
			labels.push(`${count} ${label}`);
			phrases.push(`one per ${unit} for ${count} ${count === 1 ? unit : `${unit}s`}`);
		}
	}
	const maxAgeDays = job.maxAgeDays ?? 0;
	if (maxAgeDays > 0) {
		labels.push(`${maxAgeDays} ${maxAgeDays === 1 ? 'day' : 'days'}`);
		phrases.push(`everything from the last ${maxAgeDays === 1 ? 'day' : `${maxAgeDays} days`}`);
	}
	if (labels.length === 0) {
		return { label: 'All', description: 'Keeps every snapshot this schedule takes.' };
	}
	const label = labels.join(', ');
	return {
		label: label.charAt(0).toUpperCase() + label.slice(1),
		description: `Keeps ${listPhrase(phrases)}. Older snapshots from this schedule are deleted.`
	};
}

export function runTime(date: Date | null | undefined): number {
	return date instanceof Date && !Number.isNaN(date.getTime()) && date.getFullYear() > 1
		? date.getTime()
		: 0;
}

export function nextRunLabel(job: PeriodicSnapshot, now = Date.now()): string {
	const next = runTime(job.nextRunAt);
	if (!next) return 'Not Reported';
	const remaining = next - now;
	if (remaining <= 0) return 'Due now';
	if (remaining < 60_000) return 'In under a minute';
	const label = dateToAgo(new Date(next));
	return label.charAt(0).toUpperCase() + label.slice(1);
}

export function scheduleDeleteErrorMessage(message: string): string {
	if (/periodic_snapshot_job_\d+_not_found/.test(message))
		return 'One of these schedules was already deleted. Refresh and try again.';
	return message;
}

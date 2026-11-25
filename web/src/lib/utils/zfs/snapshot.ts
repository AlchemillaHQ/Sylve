/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { SnapshotSummary } from '$lib/types/zfs/dataset';
import { escapeHTML } from '$lib/utils/string';
import { cronToHuman } from '$lib/utils/time';

function rowProperty(data: Record<string, unknown>, key: string): string {
	const properties = data.properties as Record<string, { value?: unknown } | undefined> | undefined;
	const value = properties?.[key]?.value;
	return typeof value === 'string' ? value : '';
}

export function snapshotRowCreation(data: Record<string, unknown>): number | null {
	const seconds = Number(rowProperty(data, 'creation'));
	return seconds > 0 ? seconds : null;
}

export function snapshotRowSummary(data: Record<string, unknown>): SnapshotSummary {
	const name = String(data.name ?? '');
	const at = name.indexOf('@');
	const dataset = at >= 0 ? name.slice(0, at) : name;
	return {
		name,
		dataset,
		snapshot: at >= 0 ? name.slice(at + 1) : '',
		pool: typeof data.pool === 'string' && data.pool ? data.pool : dataset.split('/')[0],
		guid: String(data.guid ?? ''),
		createdAt: new Date((snapshotRowCreation(data) ?? 0) * 1000),
		createTxg: 0,
		used: Number(data.used) || 0,
		referenced: Number(data.referenced) || 0,
		clones: rowProperty(data, 'clones').split(',').filter(Boolean),
		holds: Number(rowProperty(data, 'userrefs')) || 0
	};
}

export function snapshotRowMarkers(data: Record<string, unknown>): string {
	const { clones, holds } = snapshotRowSummary(data);
	let markers = '';
	if (clones.length > 0) {
		markers += `<span class="icon-[lucide--git-fork] text-muted-foreground size-3.5 shrink-0" title="Cloned to ${escapeHTML(clones.join(', '))}"></span>`;
	}
	if (holds > 0) {
		markers += `<span class="icon-[lucide--lock-keyhole] text-muted-foreground size-3.5 shrink-0" title="${holds} ${holds === 1 ? 'hold' : 'holds'}"></span>`;
	}
	return markers;
}

export const snapshotSchedulePresets = [
	{ value: '60', label: 'Every Minute' },
	{ value: '3600', label: 'Every Hour' },
	{ value: '86400', label: 'Every Day' },
	{ value: '604800', label: 'Every Week' },
	{ value: 'cron:0 0 1 * *', label: 'Every Month' },
	{ value: 'cron:0 0 1 1 *', label: 'Every Year' }
];

export function presetToSchedule(value: string): { interval: number; cronExpr: string } {
	if (value.startsWith('cron:')) return { interval: 0, cronExpr: value.slice(5) };
	return { interval: Number(value) || 0, cronExpr: '' };
}

export function scheduleToPreset(interval: number, cronExpr: string): string | undefined {
	const value = cronExpr.trim() ? `cron:${cronExpr.trim()}` : String(interval);
	return snapshotSchedulePresets.find((preset) => preset.value === value)?.value;
}

function intervalLabel(seconds: number): string {
	const units: [number, string][] = [
		[86400, 'Day'],
		[3600, 'Hour'],
		[60, 'Minute']
	];
	for (const [size, unit] of units) {
		if (seconds % size === 0) {
			const count = seconds / size;
			return count === 1 ? `Every ${unit}` : `Every ${count} ${unit}s`;
		}
	}
	return `Every ${seconds} Seconds`;
}

export function snapshotScheduleLabel(interval: number, cronExpr: string): string {
	const preset = scheduleToPreset(interval, cronExpr);
	if (preset) return snapshotSchedulePresets.find((entry) => entry.value === preset)!.label;
	if (cronExpr.trim()) return cronToHuman(cronExpr.trim());
	return interval > 0 ? intervalLabel(interval) : 'None';
}

export function snapshotCreateErrorMessage(message: string, name: string): string {
	if (/already exists/i.test(message)) return `A snapshot named ${name} already exists.`;
	if (/snapshot_namespace_reserved/.test(message))
		return 'This snapshot name is reserved for Sylve.';
	if (/snapshot_creation_blocked/.test(message))
		return 'Snapshots are paused for this file system while a replication or migration is running.';
	return message;
}

export function snapshotRollbackErrorMessage(message: string): string {
	if (/newer_snapshots_changed|more recent snapshots/i.test(message))
		return 'A snapshot was taken or deleted while this dialog was open. Close it and try again.';
	if (/cannot_roll_back_managed_dataset/.test(message))
		return 'Sylve manages this file system. Roll it back from its virtual machine or jail.';
	if (/cannot_roll_back_system_filesystem/.test(message))
		return 'This file system belongs to the operating system. Use a boot environment to restore it.';
	if (/download_storage_in_use/.test(message))
		return 'Sylve keeps downloads on this file system, so it can’t be rolled back while they’re in use.';
	if (/dataset_\S+_in_use\b/.test(message))
		return 'A container or a running virtual machine is using this file system.';
	if (/dependent clones/i.test(message))
		return 'A newer snapshot has clones. Delete or promote them first.';
	if (/\bheld\b|\bhold\b/i.test(message))
		return 'A newer snapshot is held, usually by a backup or transfer in progress.';
	if (/\bbusy\b/i.test(message)) return 'The file system is busy. Try again in a moment.';
	if (/dataset_identity_changed|_not_found|does not exist/i.test(message))
		return 'This snapshot changed or no longer exists. Close this dialog and try again.';
	if (/replication|protected/i.test(message))
		return 'Replication is using this file system, so it can’t be rolled back right now.';
	return message;
}

export function snapshotDeleteErrorMessage(message: string): string {
	if (/dependent clones/i.test(message))
		return 'A file system was cloned from one of these snapshots. Delete or promote the clone first.';
	if (/\bheld\b|\bhold\b/i.test(message))
		return 'One of these snapshots is held, usually by a backup or transfer in progress.';
	if (/\bbusy\b/i.test(message)) return 'One of these snapshots is busy. Try again in a moment.';
	if (/dataset_identity_changed|_not_found|does not exist/i.test(message))
		return 'The snapshots changed while this dialog was open. Close it and try again.';
	if (/replication|protected/i.test(message))
		return 'Replication is using one of these file systems, so its snapshots can’t be deleted right now.';
	return message;
}

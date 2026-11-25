/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { listDisksResult } from '$lib/api/disk/disk';
import {
	getPoolsResult,
	getPoolStatusResult,
	listPoolScrubRuns,
	listPoolScrubSchedules
} from '$lib/api/zfs/pool';
import type { Disk, Partition } from '$lib/types/disk/disk';
import { isPoolScrubActive, type Zpool, type ZpoolStatusPool } from '$lib/types/zfs/pool';
import { zpoolUseableDisks, zpoolUseablePartitions } from '$lib/utils/disk';
import { APIRequestError, isAPIResponse } from '$lib/utils/http';
import { isPoolScanActive } from '$lib/utils/zfs/pool';
import { deviceLeaves, roles, topologyGroups, type DeviceNode } from './pools';

export const booleanPoolSettings = [
	{
		key: 'autoexpand',
		label: 'Auto Expand',
		description: 'Use expanded device capacity when the VDEV can grow.'
	},
	{
		key: 'autotrim',
		label: 'Auto TRIM',
		description: 'Reclaim unused blocks on devices that support TRIM.'
	},
	{
		key: 'delegation',
		label: 'Delegation',
		description: 'Allow delegated ZFS dataset permissions.'
	},
	{
		key: 'autoreplace',
		label: 'Auto Replace',
		description: 'Allow ZFS to replace a device detected in the same physical location.'
	}
] as const;

export const failModes = [
	{ value: 'wait', label: 'Wait' },
	{ value: 'continue', label: 'Continue' },
	{ value: 'panic', label: 'Panic' }
];

export type PoolEditValues = Record<(typeof booleanPoolSettings)[number]['key'], string> & {
	failmode: string;
	comment: string;
};

export function poolEditValues(pool: Zpool): PoolEditValues {
	return {
		autoexpand: pool.properties.autoexpand?.value ?? '',
		autotrim: pool.properties.autotrim?.value ?? '',
		delegation: pool.properties.delegation?.value ?? '',
		autoreplace: pool.properties.autoreplace?.value ?? '',
		failmode: pool.properties.failmode?.value ?? '',
		comment: pool.properties.comment?.value === '-' ? '' : (pool.properties.comment?.value ?? '')
	};
}

export function changedPoolSettings(
	before: PoolEditValues,
	after: PoolEditValues
): Record<string, string> {
	return Object.fromEntries(
		(Object.keys(before) as Array<keyof PoolEditValues>)
			.filter((key) => before[key] !== after[key])
			.map((key) => [key, after[key]])
	);
}

export function poolEditBlockReason(pool: Zpool, status: ZpoolStatusPool | null): string {
	if (isPoolScanActive(status?.scan_stats))
		return 'Wait for the current scrub or resilver to finish before editing this pool.';
	const replacing = (nodes: DeviceNode[]): boolean =>
		nodes.some((node) => node.kind.toLowerCase() === 'replacing' || replacing(node.children));
	if (topologyGroups(pool, status, []).some((group) => replacing(group.nodes)))
		return 'Wait for the current device replacement to finish before editing this pool.';
	if (
		['FAULTED', 'UNAVAIL', 'UNAVAILABLE', 'OFFLINE', 'REMOVED', 'SUSPENDED'].includes(
			(status?.state || pool.state).toUpperCase()
		)
	)
		return 'The pool is unavailable for editing.';
	return '';
}

export function poolSupportsSpares(pool: Zpool): boolean {
	const walk = (nodes: DeviceNode[]): boolean =>
		nodes.some((node) => /^(mirror|raidz)/i.test(node.kind) || walk(node.children));
	return topologyGroups(pool, null, []).some((group) => group.key === 'vdevs' && walk(group.nodes));
}

export interface PoolEditSnapshot {
	pool: Zpool;
	pools: Zpool[];
	status: ZpoolStatusPool | null;
	disks: Disk[] | null;
}

export async function readPoolEditSnapshot(
	hostname: string,
	guid: string,
	signal: AbortSignal,
	includeDisks = true
) {
	const options = { hostname, signal };
	const [pools, status, runs, schedules, disks] = await Promise.all([
		getPoolsResult(false, options),
		getPoolStatusResult(guid, options),
		listPoolScrubRuns(guid, options),
		listPoolScrubSchedules(options),
		includeDisks ? listDisksResult('none', options) : Promise.resolve(null)
	]);
	signal.throwIfAborted();
	if (isAPIResponse(pools)) throw new APIRequestError(pools);
	if (isAPIResponse(status)) throw new APIRequestError(status);
	if (isAPIResponse(runs)) throw new APIRequestError(runs);
	if (isAPIResponse(schedules)) throw new APIRequestError(schedules);
	const pool = pools.find((item) => item.guid === guid);
	if (!pool) throw new Error('This pool is no longer selected in Services or is unavailable.');
	if (
		status.pool_guid !== guid ||
		status.name !== pool.name ||
		runs.some((run) => run.poolGuid !== guid)
	)
		throw new Error('The pool identity changed. Close the editor and reopen it.');
	const blocked = poolEditBlockReason(pool, status);
	if (blocked) throw new Error(blocked);
	const scanState = status.scan_stats?.state?.toUpperCase();
	const idleRoot =
		!status.scan_stats &&
		Object.values(status.vdevs).some(
			(device) => device.vdev_type === 'root' && device.guid === guid
		);
	if (!idleRoot && !['NONE', 'FINISHED', 'CANCELED', 'CANCELLED'].includes(scanState ?? ''))
		throw new Error('Current pool scan activity could not be verified. Refresh and try again.');
	if (
		runs.some((run) => isPoolScrubActive(run.status)) ||
		schedules.some(
			(schedule) =>
				schedule.poolGuid === guid && (schedule.active || isPoolScrubActive(schedule.lastStatus))
		)
	)
		throw new Error('Wait for the current scrub to finish before editing this pool.');
	return { pool, pools, status, disks: Array.isArray(disks) ? disks : null };
}

export function spareDevicePath(value: string): string | null {
	const name = value.replace(/^\/dev\//, '');
	if (
		!/^[a-z][a-z0-9_.:/-]*$/i.test(name) ||
		name.split('/').some((part) => part === '.' || part === '..' || part === '')
	)
		return null;
	return `/dev/${name}`;
}

export function currentPoolSpares(pool: Zpool, status: ZpoolStatusPool | null = null) {
	const inventory = Object.entries(pool.spares ?? {});
	const groups = topologyGroups(pool, status, []);
	const devices = groups.find((group) => group.key === 'spares')?.nodes ?? [];
	const members = groups
		.filter((group) => group.key !== 'spares')
		.flatMap((group) => deviceLeaves(group.nodes));
	return devices.map((node) => {
		const device = inventory.find(([key, item]) => {
			if (node.guid && item.guid) return node.guid === item.guid;
			const paths = [node.path, node.name].map(spareDevicePath).filter(Boolean);
			return [item.path, item.name, key].some(
				(value) => value && paths.includes(spareDevicePath(value))
			);
		})?.[1];
		return {
			path: device?.path ? spareDevicePath(device.path) : null,
			label: device?.path || node.path || node.name,
			guid: device?.guid || node.guid,
			state:
				[device?.state, node.state].some((value) => value?.toUpperCase() === 'INUSE') ||
				(node.guid && members.some((member) => member.guid === node.guid))
					? 'INUSE'
					: node.state,
			size: device?.size || node.size || 0
		};
	});
}

export function sameSparePaths(left: string[], right: string[]): boolean {
	if (left.length !== right.length) return false;
	const sortedRight = [...right].sort();
	return [...left].sort().every((path, index) => path === sortedRight[index]);
}

function partitionPaths(partition: Partition): string[] {
	return [
		spareDevicePath(partition.name),
		partition.uuid ? spareDevicePath(`gptid/${partition.uuid}`) : null
	].filter((path): path is string => path !== null);
}

function deviceAliases(path: string, disks: Disk[]): Set<string> {
	const aliases = new Set([path]);
	for (const disk of disks) {
		if (spareDevicePath(disk.device) === path)
			disk.partitions.flatMap(partitionPaths).forEach((value) => aliases.add(value));
		for (const partition of disk.partitions) {
			const paths = partitionPaths(partition);
			if (paths.includes(path)) paths.forEach((value) => aliases.add(value));
		}
	}
	return aliases;
}

export interface PoolDeviceChoice {
	path: string;
	identity: string;
	size: number;
	description: string;
	blocked: string;
}

export function usedPoolDevicePaths(
	pools: Zpool[],
	disks: Disk[],
	extra: (string | null | undefined)[] = []
): Set<string> {
	const used = new Set<string>();
	function markUsed(value: string | null | undefined) {
		const path = value ? spareDevicePath(value) : null;
		if (path) deviceAliases(path, disks).forEach((alias) => used.add(alias));
	}
	const walk = (nodes: NonNullable<Zpool['spares']>) => {
		for (const [key, node] of Object.entries(nodes)) {
			[node.path, node.name, key].forEach(markUsed);
			if (node.vdevs) walk(node.vdevs);
		}
	};
	for (const item of pools) {
		for (const role of roles) {
			walk(item[role.key] ?? {});
		}
	}
	extra.forEach(markUsed);
	return used;
}

export function unusedPoolDeviceChoices(snapshot: PoolEditSnapshot): PoolDeviceChoice[] {
	const { pool, pools, status } = snapshot;
	const disks = snapshot.disks ?? [];
	const leaves = topologyGroups(pool, status, disks).flatMap((group) => deviceLeaves(group.nodes));
	const used = usedPoolDevicePaths(
		pools,
		disks,
		leaves.flatMap((device) => [device.path, device.name])
	);
	const choices: PoolDeviceChoice[] = [];
	function add(
		path: string | null,
		size: number,
		identity: string,
		description: string,
		stable: boolean
	) {
		if (!path || size <= 0 || used.has(path) || choices.some((choice) => choice.path === path))
			return;
		choices.push({
			path,
			size,
			identity,
			description,
			blocked: stable ? '' : 'A stable device identity is unavailable.'
		});
	}
	for (const disk of zpoolUseableDisks(disks)) {
		if (disk.partitions.length > 0 || /^(?:a?cd)\d+$/.test(disk.device)) continue;
		add(
			spareDevicePath(disk.device),
			disk.size,
			`${disk.uuid}|${disk.serial}|${disk.size}`,
			[disk.model, disk.serial].filter(Boolean).join(' · ') || disk.type,
			disk.identityStable
		);
	}
	for (const partition of zpoolUseablePartitions(disks, pools)) {
		const disk = disks.find((item) => item.partitions.some((part) => part.name === partition.name));
		if (!disk || disk.partitions.some((part) => /boot|efi|swap/i.test(part.usage))) continue;
		if (!['', 'unused', 'unused entry', 'zfs'].includes(partition.usage.toLowerCase())) continue;
		add(
			spareDevicePath(partition.name),
			partition.size,
			`${partition.uuid}|${disk.uuid}|${disk.serial}|${partition.size}`,
			[disk.device, disk.model, 'Partition'].filter(Boolean).join(' · '),
			Boolean(partition.uuid) || disk.identityStable
		);
	}
	return choices;
}

export function poolSpareChoices(snapshot: PoolEditSnapshot): PoolDeviceChoice[] {
	const { pool, status } = snapshot;
	const members = topologyGroups(pool, status, snapshot.disks ?? [])
		.filter((group) => group.key !== 'spares' && group.key !== 'l2cache')
		.flatMap((group) => deviceLeaves(group.nodes));
	const minimum =
		members.length > 0 && members.every((device) => device.size !== null)
			? Math.max(...members.map((device) => device.size!))
			: null;
	return [
		...currentPoolSpares(pool, status).map((device) => ({
			path: device.path ?? device.label,
			identity: device.guid,
			size: device.size,
			description: `Current spare · ${device.state || 'Unknown state'}`,
			blocked:
				!device.path || !device.guid
					? 'The device identity or path could not be verified.'
					: device.state.toUpperCase() === 'INUSE'
						? 'This spare is in use.'
						: ''
		})),
		...unusedPoolDeviceChoices(snapshot).map((choice) => ({
			...choice,
			blocked:
				choice.blocked ||
				(minimum === null
					? 'Pool device sizes are unavailable.'
					: choice.size < minimum
						? 'Smaller than the pool’s largest device.'
						: '')
		}))
	];
}

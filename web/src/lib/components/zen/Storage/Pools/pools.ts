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
import type { Disk } from '$lib/types/disk/disk';
import type { Zpool, ZpoolStatusPool, ZpoolStatusVDEV, ZpoolVdev } from '$lib/types/zfs/pool';

export interface PoolsData {
	hostname: string;
	pools: Zpool[] | APIResponse;
	disks: Disk[] | APIResponse;
	statuses?: Record<string, ZpoolStatusPool>;
	fresh?: { pools: boolean; disks: boolean };
}

export interface DeviceNode {
	id: string;
	guid: string;
	name: string;
	path: string;
	kind: string;
	state: string;
	reported: boolean;
	size: number | null;
	replacementSize: number | null;
	errors: { read: number | null; write: number | null; checksum: number | null };
	disk?: Disk;
	children: DeviceNode[];
}

export const roles = [
	{ key: 'vdevs', label: 'Data VDEVs', icon: 'icon-[bi--hdd-stack-fill]' },
	{ key: 'logs', label: 'Logs (ZIL)', icon: 'icon-[icon-park-outline--log]' },
	{ key: 'l2cache', label: 'Cache (L2ARC)', icon: 'icon-[octicon--cache-16]' },
	{ key: 'spares', label: 'Spares', icon: 'icon-[bi--hdd-stack-fill]' },
	{ key: 'special', label: 'Special', icon: 'icon-[carbon--data-table]' },
	{ key: 'dedup', label: 'Dedup', icon: 'icon-[carbon--data-set]' }
] as const;

export function poolHref(hostname: string, guid = ''): string {
	const path = `/${encodeURIComponent(hostname)}/storage/zfs/pools`;
	return guid ? `${path}?${new URLSearchParams({ pool: guid })}` : path;
}

export function poolCreateHref(hostname: string): string {
	return `${poolHref(hostname)}?create`;
}

export function poolUsage(pool: Pick<Zpool, 'size' | 'allocated'>): number {
	return pool.size > 0 ? (pool.allocated / pool.size) * 100 : 0;
}

export function reportedNumber(value: string | number | null | undefined): number | null {
	if (value === undefined || value === null || (typeof value === 'string' && !value.trim()))
		return null;
	const number = Number(value);
	return Number.isFinite(number) && number >= 0 ? number : null;
}

export function vdevLabel(kind: string, name = ''): string {
	const type = kind.toLowerCase();
	if (type === 'mirror') return 'Mirror';
	if (type.startsWith('raidz')) {
		const level = type.match(/^raidz([123])/)?.[1] ?? name.match(/^raidz([123])/)?.[1] ?? '1';
		return level === '1' ? 'RAIDZ' : `RAIDZ${level}`;
	}
	if (type === 'disk' || type === 'file') return type === 'file' ? 'File' : 'Disk';
	return type ? type[0].toUpperCase() + type.slice(1) : 'Unknown';
}

export function poolLayout(pool: Zpool): string {
	const layouts = new Set(
		Object.values(pool.vdevs)
			.filter((vdev) => vdev.vdev_type !== 'indirect')
			.map((vdev) =>
				['disk', 'file'].includes(vdev.vdev_type.toLowerCase())
					? 'Stripe'
					: vdevLabel(vdev.vdev_type, vdev.name)
			)
	);
	return layouts.size > 1 ? 'Mixed' : (layouts.values().next().value ?? 'Unknown');
}

function deviceName(value: string): string {
	return value.replace(/^\/dev\//, '');
}

function diskFor(paths: string[], disks: Disk[]): Disk | undefined {
	const names = new Set(paths.filter(Boolean).map(deviceName));
	return disks.find(
		(disk) =>
			names.has(deviceName(disk.device)) ||
			disk.partitions.some(
				(part) => names.has(deviceName(part.name)) || (part.uuid && names.has(`gptid/${part.uuid}`))
			)
	);
}

function matches(
	inventory: ZpoolVdev,
	status: ZpoolStatusVDEV,
	key: string,
	statusKey: string
): boolean {
	if (inventory.guid && status.guid) return inventory.guid === status.guid;
	const names = [inventory.path, inventory.name, key]
		.filter((value): value is string => Boolean(value))
		.map(deviceName);
	return [status.path, status.name, statusKey].some(
		(value) => value && names.includes(deviceName(value))
	);
}

function nodes(
	inventory: Record<string, ZpoolVdev> | null | undefined,
	status: Record<string, ZpoolStatusVDEV> | null | undefined,
	disks: Disk[],
	parent: string
): DeviceNode[] {
	const remaining = new Map(Object.entries(inventory ?? {}));
	const pairs: Array<{ key: string; inventory?: ZpoolVdev; status?: ZpoolStatusVDEV }> = [];
	for (const [key, value] of Object.entries(status ?? {})) {
		const match = [...remaining].find(([inventoryKey, candidate]) =>
			matches(candidate, value, inventoryKey, key)
		);
		if (match) remaining.delete(match[0]);
		pairs.push({ key, inventory: match?.[1], status: value });
	}
	for (const [key, value] of remaining) pairs.push({ key, inventory: value });
	return pairs.map(({ key, inventory: item, status: detail }) => {
		const guid = detail?.guid || item?.guid || '';
		const id = `${parent}/${encodeURIComponent(guid || key)}`;
		const name = detail?.name || item?.name || key;
		const path = detail?.path || item?.path || '';
		const children = nodes(item?.vdevs, detail?.vdevs, disks, id);
		return {
			id,
			guid,
			name,
			path,
			kind: detail?.vdev_type || item?.vdev_type || '',
			state: detail?.state || item?.state || 'UNKNOWN',
			reported: Boolean(detail?.guid && detail.vdev_type && detail.state),
			size:
				reportedNumber(detail?.total_space) ||
				reportedNumber(detail?.rep_dev_size) ||
				reportedNumber(item?.size) ||
				null,
			replacementSize:
				reportedNumber(detail?.rep_dev_size) ||
				reportedNumber(detail?.total_space) ||
				reportedNumber(item?.size) ||
				null,
			errors: {
				read: reportedNumber(detail?.read_errors),
				write: reportedNumber(detail?.write_errors),
				checksum: reportedNumber(detail?.checksum_errors)
			},
			disk:
				children.length === 0
					? diskFor([path, name, item?.path || '', item?.name || '', key], disks)
					: undefined,
			children
		};
	});
}

export function topologyGroups(pool: Zpool, status: ZpoolStatusPool | null, disks: Disk[]) {
	const root = Object.entries(status?.vdevs ?? {}).find(
		([, vdev]) => vdev.vdev_type === 'root' && (vdev.guid === pool.guid || vdev.name === pool.name)
	);
	const data: Record<string, ZpoolVdev> = root
		? {
				[root[0]]: {
					name: pool.name,
					guid: pool.guid,
					vdev_type: 'root',
					class: 'data',
					state: pool.state,
					size: pool.size,
					free: pool.free,
					allocated: pool.allocated,
					vdevs: pool.vdevs
				}
			}
		: pool.vdevs;
	return roles
		.map((role) => ({
			...role,
			nodes: nodes(
				role.key === 'vdevs' ? data : pool[role.key],
				status?.[role.key],
				disks,
				role.key
			)
		}))
		.filter((role) => role.nodes.length > 0);
}

export function deviceLeaves(values: DeviceNode[]): DeviceNode[] {
	return values.flatMap((node) =>
		node.children.length > 0
			? deviceLeaves(node.children)
			: ['indirect', 'root'].includes(node.kind.toLowerCase())
				? []
				: [node]
	);
}

export function poolDeviceCount(pool: Zpool): number {
	return topologyGroups(pool, null, []).reduce(
		(total, group) => total + deviceLeaves(group.nodes).length,
		0
	);
}

export function deviceErrorSummary(values: DeviceNode[]) {
	const devices = deviceLeaves(values);
	const poolRoots = values.filter((node) => node.kind.toLowerCase() === 'root');
	const branches = values.flatMap((node) =>
		node.kind.toLowerCase() === 'root' ? node.children : [node]
	);
	const totals = { read: 0, write: 0, checksum: 0 };
	function count(node: DeviceNode, key: keyof typeof totals): number {
		const children = node.children.reduce((sum, child) => sum + count(child, key), 0);
		return Math.max(node.errors[key] ?? 0, children);
	}
	for (const key of ['read', 'write', 'checksum'] as const) {
		totals[key] = Math.max(
			branches.reduce((sum, node) => sum + count(node, key), 0),
			...poolRoots.map((root) => root.errors[key] ?? 0)
		);
	}
	const complete =
		devices.length > 0 &&
		devices.every((device) => Object.values(device.errors).every((value) => value !== null));
	return { ...totals, complete, total: totals.read + totals.write + totals.checksum };
}

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
import type { VdevType, Zpool } from '$lib/types/zfs/pool';
import { getAPIErrorMessages } from '$lib/utils/http';
import { parsePoolActionError } from '$lib/utils/zfs/pool.svelte';
import { diskIcon } from '../Disks/disks';
import { spareDevicePath, usedPoolDevicePaths } from './edit';

export type Layout = 'stripe' | 'mirror' | 'raidz' | 'raidz2' | 'raidz3';

export interface CreateDevice {
	path: string;
	name: string;
	size: number;
	type: string;
	partition: boolean;
	description: string;
	blocked: string;
}

export interface DraftVdev {
	key: number;
	role: VdevType;
	layout: Layout;
	devices: string[];
}

export const roleOptions: { value: VdevType; label: string }[] = [
	{ value: 'data', label: 'Data' },
	{ value: 'log', label: 'Log (ZIL)' },
	{ value: 'cache', label: 'Cache (L2ARC)' },
	{ value: 'special', label: 'Special' },
	{ value: 'dedup', label: 'Dedup' }
];

export const layoutOptions: { value: Layout; label: string; minimum: number; parity: number }[] = [
	{ value: 'stripe', label: 'Stripe', minimum: 1, parity: 0 },
	{ value: 'mirror', label: 'Mirror', minimum: 2, parity: 0 },
	{ value: 'raidz', label: 'RAIDZ', minimum: 3, parity: 1 },
	{ value: 'raidz2', label: 'RAIDZ2', minimum: 4, parity: 2 },
	{ value: 'raidz3', label: 'RAIDZ3', minimum: 5, parity: 3 }
];

export function layoutsFor(role: VdevType) {
	if (role === 'cache') return layoutOptions.filter((layout) => layout.value === 'stripe');
	if (role === 'log')
		return layoutOptions.filter((layout) => ['stripe', 'mirror'].includes(layout.value));
	return layoutOptions;
}

export function layoutLabel(layout: Layout): string {
	return layoutOptions.find((option) => option.value === layout)?.label ?? 'Stripe';
}

export function deviceIcon(device: CreateDevice): string {
	return device.partition ? 'icon-[ant-design--partition-outlined]' : diskIcon(device);
}

export function roleLabel(role: VdevType): string {
	return roleOptions.find((option) => option.value === role)?.label ?? 'Data';
}

const zfsPartitionTypes = ['', 'unused', 'unused entry', 'zfs'];
const protectedPartition = /boot|efi|swap/i;
const sizeTolerance = 16 * 1024 * 1024;

export function createDeviceChoices(pools: Zpool[], disks: Disk[]): CreateDevice[] {
	const used = usedPoolDevicePaths(pools, disks);
	const choices: CreateDevice[] = [];
	for (const disk of disks) {
		const diskPath = spareDevicePath(disk.device);
		if (!diskPath || disk.size <= 0 || /^(?:a?cd)\d+$/.test(disk.device)) continue;
		if (disk.partitions.length === 0) {
			if (disk.usage === 'ZFS' || used.has(diskPath)) continue;
			choices.push({
				path: diskPath,
				name: disk.device,
				size: disk.size,
				type: disk.type,
				partition: false,
				description: [disk.type, disk.model.trim()].filter(Boolean).join(' · '),
				blocked: disk.gpt
					? 'Has an empty GPT partition table. Clear it or create a partition on it first.'
					: ''
			});
			continue;
		}
		const protectedDisk = disk.partitions.some((partition) =>
			protectedPartition.test(partition.usage)
		);
		for (const partition of disk.partitions) {
			const path = spareDevicePath(partition.name);
			if (
				!path ||
				partition.size <= 0 ||
				used.has(path) ||
				protectedPartition.test(partition.usage)
			)
				continue;
			choices.push({
				path,
				name: partition.name,
				size: partition.size,
				type: disk.type,
				partition: true,
				description: ['Partition', disk.type, partition.usage].filter(Boolean).join(' · '),
				blocked: protectedDisk
					? 'Its disk has a boot, EFI or swap partition.'
					: zfsPartitionTypes.includes(partition.usage.toLowerCase())
						? ''
						: `Only ZFS partitions can be used. This one is ${partition.usage}.`
			});
		}
	}
	return choices.sort(
		(a, b) =>
			Number(Boolean(a.blocked)) - Number(Boolean(b.blocked)) ||
			a.name.localeCompare(b.name, undefined, { numeric: true })
	);
}

export function nameProblem(name: string, pools: Zpool[]): string {
	const value = name.trim();
	if (!value) return 'Enter a name for the pool';
	if (value.length > 24) return 'Use at most 24 characters';
	if (!/^[a-zA-Z][a-zA-Z0-9]*$/.test(value))
		return 'Use letters and digits only, starting with a letter';
	if (/^(log|mirror|raidz|spare)/.test(value) || /^c[0-9]/.test(value)) {
		return 'Names starting with log, mirror, raidz, spare, or c and a digit are reserved by ZFS';
	}
	if (pools.some((pool) => pool.name.toLowerCase() === value.toLowerCase()))
		return 'A pool with this name already exists';
	return '';
}

function sizesDiffer(devices: CreateDevice[]): boolean {
	const sizes = devices.map((device) => device.size);
	return sizes.length > 1 && Math.max(...sizes) - Math.min(...sizes) >= sizeTolerance;
}

export function vdevProblem(vdev: DraftVdev, devices: CreateDevice[], force: boolean): string {
	const layout = layoutOptions.find((option) => option.value === vdev.layout) ?? layoutOptions[0];
	if (devices.length === 0) return 'Add at least one device.';
	if (devices.length < layout.minimum)
		return `${layout.label} needs at least ${layout.minimum} devices.`;
	if (vdev.layout !== 'stripe' && !force && sizesDiffer(devices)) {
		return 'Device sizes differ, which ZFS refuses for this layout. Match them or turn on Force Create.';
	}
	return '';
}

export function vdevWarning(devices: CreateDevice[]): string {
	if (new Set(devices.map((device) => device.type)).size > 1) {
		return 'Mixes drive types, so this VDEV runs at the speed of its slowest device.';
	}
	if (sizesDiffer(devices))
		return 'Device sizes differ; each device only contributes as much as the smallest.';
	return '';
}

export function spareSupport(vdevs: DraftVdev[]): boolean {
	return vdevs.some((vdev) => vdev.role === 'data' && vdev.layout !== 'stripe');
}

export function poolProblems(
	vdevs: DraftVdev[],
	spares: CreateDevice[],
	devices: Map<string, CreateDevice>,
	force: boolean
): string[] {
	const problems: string[] = [];
	const data = vdevs.filter((vdev) => vdev.role === 'data');
	if (data.length === 0) problems.push('Add at least one Data VDEV.');
	if (new Set(data.map((vdev) => vdev.layout)).size > 1 && !force) {
		problems.push('Data VDEVs use different layouts. Use one layout or turn on Force Create.');
	}
	const allocation = vdevs.filter((vdev) => vdev.role === 'special' || vdev.role === 'dedup');
	if (new Set(allocation.map((vdev) => vdev.layout)).size > 1) {
		problems.push('Special and Dedup VDEVs must use the same layout.');
	}
	if (spares.length > 0) {
		if (!spareSupport(vdevs)) problems.push('Spares need a Mirror or RAIDZ Data VDEV.');
		const members = vdevs.flatMap((vdev) =>
			vdev.devices.map((path) => devices.get(path)?.size ?? 0)
		);
		const largest = members.length > 0 ? Math.max(...members) : 0;
		if (spares.some((spare) => spare.size < largest)) {
			problems.push('Spares must be at least as large as the largest device in the pool.');
		}
	}
	return problems;
}

export function usableEstimate(vdevs: DraftVdev[], devices: Map<string, CreateDevice>): number {
	let total = 0;
	for (const vdev of vdevs) {
		if (vdev.role !== 'data') continue;
		const sizes = vdev.devices.map((path) => devices.get(path)?.size ?? 0);
		const layout = layoutOptions.find((option) => option.value === vdev.layout) ?? layoutOptions[0];
		if (sizes.length < layout.minimum) continue;
		const smallest = Math.min(...sizes);
		if (vdev.layout === 'stripe') total += sizes.reduce((sum, size) => sum + size, 0);
		else if (vdev.layout === 'mirror') total += smallest;
		else total += (sizes.length - layout.parity) * smallest;
	}
	return total;
}

const createErrors: Record<string, string> = {
	pool_name_taken: 'A pool with this name already exists.',
	invalid_pool_name: 'This pool name is not allowed.',
	invalid_pool_mountpoint: 'The mount point is not valid.',
	at_least_one_data_vdev_required: 'Add at least one Data VDEV.'
};

export function createError(response: APIResponse): string {
	const text = getAPIErrorMessages(response).join(', ');
	return (
		createErrors[text] ??
		parsePoolActionError({ ...response, message: response.message || 'pool_create_failed' })
	);
}

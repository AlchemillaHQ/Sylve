/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { Disk } from '$lib/types/disk/disk';
import type { PoolDeviceMutationOptions, Zpool, ZpoolStatusPool } from '$lib/types/zfs/pool';
import {
	poolEditBlockReason,
	spareDevicePath,
	unusedPoolDeviceChoices,
	type PoolEditSnapshot
} from './edit';
import { deviceLeaves, roles, topologyGroups, type DeviceNode } from './pools';

export type PoolDeviceAction = 'attach' | 'replace' | 'detach';

export interface PoolDeviceTarget {
	role: (typeof roles)[number]['key'];
	node: DeviceNode;
	ancestors: DeviceNode[];
}

export function poolDeviceTargets(pool: Zpool, status: ZpoolStatusPool | null, disks: Disk[]) {
	const targets: PoolDeviceTarget[] = [];
	for (const group of topologyGroups(pool, status, disks)) {
		function walk(nodes: DeviceNode[], ancestors: DeviceNode[]) {
			for (const node of nodes) {
				if (node.children.length > 0) walk(node.children, [...ancestors, node]);
				else targets.push({ role: group.key, node, ancestors });
			}
		}
		walk(group.nodes, []);
	}
	return targets;
}

export function findPoolDeviceTarget(snapshot: PoolEditSnapshot, original: PoolDeviceTarget) {
	const matches = poolDeviceTargets(snapshot.pool, snapshot.status, snapshot.disks ?? []).filter(
		(target) => target.role === original.role && target.node.guid === original.node.guid
	);
	return matches.length === 1 ? matches[0] : null;
}

export function poolDeviceFingerprint(target: PoolDeviceTarget): string {
	return JSON.stringify([
		target.role,
		target.node.guid,
		target.node.kind,
		target.node.path,
		target.node.replacementSize,
		target.ancestors.map((parent) => [
			parent.guid,
			parent.kind,
			parent.children.map((child) => `${child.guid}:${child.kind}`).sort()
		])
	]);
}

export function poolDevicePreconditions(target: PoolDeviceTarget): PoolDeviceMutationOptions {
	const parent = target.ancestors.at(-1);
	return parent
		? {
				expectedParentGuid: parent.guid,
				expectedMemberGuids: parent.children.map((member) => member.guid)
			}
		: {};
}

export function poolDeviceActionReason(
	pool: Zpool,
	status: ZpoolStatusPool | null,
	target: PoolDeviceTarget | null,
	action: PoolDeviceAction
): string {
	if (!target)
		return 'This device is no longer in the reported topology. Close this dialog and refresh.';
	const { node, role, ancestors } = target;
	if (role === 'spares')
		return 'Manage spare assignments with Edit Pool. This does not detach spares.';
	if (role === 'l2cache') return 'Cache devices do not support these mirror-member actions.';
	if (!status || status.pool_guid !== pool.guid || status.name !== pool.name)
		return 'Refresh the pool status before changing devices.';
	const blocked = poolEditBlockReason(pool, status);
	if (blocked) return blocked;
	if (!['ONLINE', 'DEGRADED'].includes(status.state.toUpperCase()))
		return 'The pool is unavailable for device changes.';
	const bootfs = pool.properties.bootfs?.value;
	if (
		(bootfs && !['-', 'none', ''].includes(bootfs.toLowerCase())) ||
		node.disk?.partitions.some((partition) => /boot|efi/i.test(partition.usage))
	)
		return 'Boot-pool devices require bootloader-aware maintenance outside this dialog.';
	if (
		!node.reported ||
		!/^\d+$/.test(node.guid) ||
		/^0+$/.test(node.guid) ||
		ancestors.some((parent) => !parent.reported)
	)
		return 'The device identity and topology must be verified from current ZFS status.';
	if (
		node.kind.toLowerCase() !== 'disk' ||
		ancestors.some((parent) => !/^(root|mirror|raidz\d*|draid\d*)$/i.test(parent.kind))
	)
		return 'This device topology is not supported for this action.';
	if (
		!['ONLINE', 'DEGRADED', 'FAULTED', 'UNAVAIL', 'UNAVAILABLE', 'OFFLINE', 'REMOVED'].includes(
			node.state.toUpperCase()
		)
	)
		return 'The source device’s current health could not be verified.';
	if (action === 'attach') {
		if (ancestors.some((parent) => !['root', 'mirror'].includes(parent.kind.toLowerCase())))
			return 'Only single-disk VDEVs and mirrors can accept a new mirror member. RAIDZ expansion is not supported here.';
		if (
			node.state.toUpperCase() !== 'ONLINE' ||
			Object.values(node.errors).some((count) => count === null || count > 0)
		)
			return 'Attach to an online existing member with verified, error-free status.';
	}
	if (action !== 'detach')
		return node.replacementSize === null ? 'The source device’s required size is unavailable.' : '';
	const parent = ancestors.at(-1);
	if (parent?.kind.toLowerCase() !== 'mirror' || parent.children.length < 2)
		return 'Only individual mirror members can be detached. RAIDZ and whole VDEVs cannot be detached.';
	const remaining = parent.children.filter((sibling) => sibling.guid !== node.guid);
	if (
		remaining.length !== parent.children.length - 1 ||
		remaining.some(
			(sibling) =>
				!sibling.reported ||
				!sibling.guid ||
				sibling.children.length > 0 ||
				sibling.kind.toLowerCase() !== 'disk' ||
				sibling.state.toUpperCase() !== 'ONLINE' ||
				Object.values(sibling.errors).some((count) => count === null || count > 0)
		)
	)
		return 'Every remaining mirror member must be online with verified, error-free status before detaching.';
	return '';
}

export function replacementDeviceChoices(snapshot: PoolEditSnapshot, target: PoolDeviceTarget) {
	const disks = snapshot.disks ?? [];
	const assigned = new Set(
		snapshot.pools.flatMap((pool) =>
			topologyGroups(pool, pool.guid === snapshot.pool.guid ? snapshot.status : null, disks)
				.flatMap((group) => deviceLeaves(group.nodes))
				.flatMap((device) => (device.disk ? [device.disk.device] : []))
		)
	);
	return unusedPoolDeviceChoices(snapshot).map((choice) => {
		const disk = disks.find(
			(item) =>
				spareDevicePath(item.device) === choice.path ||
				item.partitions.some((partition) => spareDevicePath(partition.name) === choice.path)
		);
		return {
			...choice,
			description:
				disk && spareDevicePath(disk.device) !== choice.path
					? [disk.device, disk.model, disk.serial, 'Partition'].filter(Boolean).join(' · ')
					: choice.description,
			blocked:
				choice.blocked ||
				(!disk || assigned.has(disk.device)
					? 'This physical disk is already assigned to a pool or could not be identified.'
					: target.node.replacementSize === null
						? 'The source device’s required size is unavailable.'
						: choice.size < target.node.replacementSize
							? 'Smaller than the source device’s required size.'
							: '')
		};
	});
}

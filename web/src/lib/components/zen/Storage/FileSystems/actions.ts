/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { Dataset } from '$lib/types/zfs/dataset';
import { isPoolRoot, type FileSystemRow } from './filesystems';

const systemMountpoints = ['/', '/etc', '/home', '/root', '/tmp'];
const systemMountpointTrees = ['/boot', '/usr', '/var'];

export function isSystemMountpoint(mountpoint: string): boolean {
	const trimmed = mountpoint.trim();
	if (!trimmed.startsWith('/')) return false;
	const value = trimmed.replace(/\/+$/, '') || '/';
	if (systemMountpoints.includes(value)) return true;
	return systemMountpointTrees.some((tree) => value === tree || value.startsWith(`${tree}/`));
}

export function systemDeletionReasons(
	datasets: Pick<Dataset, 'name' | 'mountpoint'>[]
): Map<string, string> {
	const reasons = new Map<string, string>();
	const system = datasets.filter((dataset) => isSystemMountpoint(dataset.mountpoint));
	for (const dataset of system) {
		reasons.set(
			dataset.name,
			`Its mount point ${dataset.mountpoint.trim()} belongs to the operating system.`
		);
	}
	for (const dataset of system) {
		const parts = dataset.name.split('/');
		for (let index = 1; index < parts.length; index++) {
			const ancestor = parts.slice(0, index).join('/');
			if (!reasons.has(ancestor)) {
				reasons.set(
					ancestor,
					`It contains ${dataset.name}, which belongs to the operating system.`
				);
			}
		}
	}
	return reasons;
}

export function deleteBlock(row: FileSystemRow, system?: Map<string, string>): string {
	if (isPoolRoot(row.dataset))
		return 'Pool root file systems can’t be deleted. Delete the pool instead.';
	return system?.get(row.dataset.name) ?? '';
}

function inside(name: string, root: string): boolean {
	return name.startsWith(`${root}/`);
}

export function independentRoots<T extends Pick<Dataset, 'name'>>(datasets: T[]): T[] {
	return datasets.filter(
		(candidate) =>
			!datasets.some((other) => other !== candidate && inside(candidate.name, other.name))
	);
}

export interface DeletionImpact {
	filesystems: number;
	volumes: number;
	snapshots: number;
}

export function deletionImpact(roots: string[], datasets: Dataset[]): DeletionImpact {
	const impact: DeletionImpact = { filesystems: 0, volumes: 0, snapshots: 0 };
	for (const dataset of datasets) {
		if (dataset.type === 'SNAPSHOT') {
			const owner = dataset.name.split('@')[0];
			if (roots.some((root) => owner === root || inside(owner, root))) impact.snapshots++;
			continue;
		}
		if (!roots.some((root) => inside(dataset.name, root))) continue;
		if (dataset.type === 'VOLUME') impact.volumes++;
		else if (dataset.type === 'FILESYSTEM') impact.filesystems++;
	}
	return impact;
}

export function impactLines(impact: DeletionImpact, single: boolean): string[] {
	const plural = (count: number, word: string) => `${count} ${word}${count === 1 ? '' : 's'}`;
	const lines: string[] = [];
	if (impact.filesystems > 0)
		lines.push(`${plural(impact.filesystems, 'file system')} inside ${single ? 'it' : 'them'}`);
	if (impact.volumes > 0) lines.push(plural(impact.volumes, 'volume'));
	if (impact.snapshots > 0) lines.push(plural(impact.snapshots, 'snapshot'));
	return lines;
}

export function deleteErrorMessage(message: string): string {
	const iscsi = message.match(/volume_(\S+)_shared_over_iscsi_by_(\S+)/);
	if (iscsi)
		return `The iSCSI target ${iscsi[2]} shares the volume ${iscsi[1]} inside it. Stop sharing the volume first.`;
	if (/in_use_by_vm/.test(message))
		return 'A virtual machine uses this file system or something inside it. Remove it from the virtual machine first.';
	if (/in_use_by_container/.test(message))
		return 'A container uses this file system or something inside it. Remove it from the container first.';
	if (/download_storage_in_use/.test(message))
		return 'Download storage on this file system is in use, so it can’t be deleted right now.';
	if (/cannot_delete_system_filesystem/.test(message))
		return 'Part of the operating system is in this file system, so it can’t be deleted.';
	if (/cannot_delete_pool_root_dataset/.test(message))
		return 'Pool root file systems can’t be deleted. Delete the pool instead.';
	if (/cannot_delete_critical_filesystem|cannot_delete_container_image_layer/.test(message))
		return 'Sylve needs this file system, so it can’t be deleted.';
	if (/dataset_identity_changed|_not_found/.test(message))
		return 'The file systems changed while this dialog was open. Close it and try again.';
	if (/\bbusy\b/i.test(message))
		return 'The file system is busy. Something may have files open in it, or it may be shared or used by a jail.';
	return message;
}

const reservedSnapshotPrefixes = ['ha_', 'bk_', 'sylve-migrate-', 'sylve_create_'];

export function defaultSnapshotName(now = new Date()): string {
	const pad = (value: number) => String(value).padStart(2, '0');
	const date = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}`;
	const time = `${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`;
	return `manual-${date}-${time}`;
}

export function snapshotNameProblem(name: string, dataset: string): string {
	const value = name.trim();
	if (!value) return 'Enter a name';
	if (!/^[a-zA-Z0-9_.:-]+$/.test(value)) return 'Use letters, digits, and - _ . or : only';
	const lower = value.toLowerCase();
	const reserved = reservedSnapshotPrefixes.find((prefix) => lower.startsWith(prefix));
	if (reserved) return `Names starting with ${reserved} are reserved for Sylve`;
	if (`${dataset}@${value}`.length > 255) return 'The full name must be at most 255 characters';
	return '';
}

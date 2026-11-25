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
import type { ISCSITarget } from '$lib/types/iscsi/target';
import type { DetailedDataset } from '$lib/types/zfs/dataset';
import { formatBytesBinary } from '$lib/utils/bytes';
import { isKeyUnavailable, isSylveDataset, propertyValue } from '../FileSystems/filesystems';

export interface VolumesData {
	hostname: string;
	datasets: DetailedDataset[] | APIResponse;
	fresh?: boolean;
}

export interface VolumeUser {
	target: string;
	lun: number;
}

export function visibleDatasets(datasets: DetailedDataset[]): DetailedDataset[] {
	return datasets
		.filter((dataset) => !isSylveDataset(dataset.name))
		.sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
}

export function parentChoices(fileSystems: DetailedDataset[]): DetailedDataset[] {
	return visibleDatasets(fileSystems);
}

export function datasetNames(...lists: DetailedDataset[][]): Set<string> {
	return new Set(lists.flat().map((dataset) => dataset.name));
}

function bytes(dataset: DetailedDataset, property: string): number {
	const value = Number(propertyValue(dataset, property));
	return Number.isFinite(value) && value > 0 ? value : 0;
}

export function volumeSize(dataset: DetailedDataset): number {
	return bytes(dataset, 'volsize');
}

export function blockSize(dataset: DetailedDataset): number {
	return bytes(dataset, 'volblocksize');
}

export function reservation(dataset: DetailedDataset): number {
	return bytes(dataset, 'refreservation');
}

export function isSparse(dataset: DetailedDataset): boolean {
	return reservation(dataset) === 0;
}

const volumeModes: Record<string, string> = {
	default: 'System Default',
	full: 'Full',
	geom: 'GEOM',
	dev: 'Device Only',
	none: 'Hidden'
};

export function volumeModeLabel(dataset: DetailedDataset): string {
	const mode = propertyValue(dataset, 'volmode');
	return mode ? (volumeModes[mode] ?? mode) : 'Not Reported';
}

export function devicePath(dataset: DetailedDataset): string {
	return `/dev/zvol/${dataset.name}`;
}

export function volumeUsers(targets: ISCSITarget[]): Map<string, VolumeUser[]> {
	const users = new Map<string, VolumeUser[]>();
	for (const target of targets) {
		for (const lun of target.luns) {
			const list = users.get(lun.zvol) ?? [];
			list.push({ target: target.targetName, lun: lun.lunNumber });
			users.set(lun.zvol, list);
		}
	}
	return users;
}

export function userLabel(user: VolumeUser): string {
	return `${user.target} (LUN ${user.lun})`;
}

export function sharedBlock(users: VolumeUser[]): string {
	const targets = [...new Set(users.map((user) => user.target))];
	if (targets.length === 0) return '';
	return targets.length === 1
		? `The iSCSI target ${targets[0]} shares it. Remove it from the target first.`
		: `The iSCSI targets ${targets.join(', ')} share it. Remove it from them first.`;
}

export function flashBlock(volume: DetailedDataset, users: VolumeUser[]): string {
	const shared = sharedBlock(users);
	if (shared) return shared;
	if (isKeyUnavailable(volume)) return 'Its encryption key is not loaded.';
	if (propertyValue(volume, 'readonly') === 'on')
		return 'It’s read only. Turn off Read Only in Edit first.';
	if (propertyValue(volume, 'volmode') === 'none')
		return 'Its volume mode is Hidden, so it has no device to write to. Change it in Edit first.';
	return '';
}

export function flashErrorMessage(message: string): string {
	const iscsi = message.match(/volume_(\S+)_shared_over_iscsi_by_(\S+)/);
	if (iscsi)
		return `The iSCSI target ${iscsi[2]} shares this volume. Remove it from the target first.`;
	if (/in_use_by_vm/.test(message))
		return 'Something in Sylve is using this volume, such as a running virtual machine.';
	if (/_is_read_only/.test(message))
		return 'The volume is read only. Turn off Read Only in Edit first.';
	if (/source_size_exceeds_volume_size/.test(message)) {
		const needed = Number(message.match(/image needs (\d+) bytes/)?.[1]);
		return needed
			? `The image needs ${formatBytesBinary(needed)}, more than the volume holds. Grow the volume first.`
			: 'The image is larger than the volume. Grow the volume first.';
	}
	if (/image_has_backing_file/.test(message))
		return 'This disk image depends on another image file, so it can’t be written on its own.';
	if (/unsupported_image_format/.test(message)) return 'Sylve can’t read this image’s format.';
	if (/source_not_found/.test(message))
		return 'The image file couldn’t be found. It may have been deleted, or its storage may be unavailable.';
	if (/zvol_not_found/.test(message))
		return 'The volume has no device to write to. Check its volume mode and encryption key.';
	if (/replication_protected_dataset_mutation_blocked/.test(message))
		return 'Replication protects this volume, so it can’t be overwritten.';
	if (/with guid \S+ not found/.test(message))
		return 'The volume changed while this dialog was open. Close it and try again.';
	return message;
}

export function volumeDeleteErrorMessage(message: string, single: boolean): string {
	const subject = single ? 'this volume' : 'one of these volumes';
	const iscsi = message.match(/volume_(\S+)_shared_over_iscsi_by_(\S+)/);
	if (iscsi)
		return `The iSCSI target ${iscsi[2]} shares ${iscsi[1]}. Remove it from the target first.`;
	if (/replication_protected_dataset_mutation_blocked/.test(message))
		return `Replication protects ${subject}, so it can’t be deleted.`;
	if (/in_use_by_vm/.test(message))
		return `A virtual machine uses ${subject}. Remove it from the virtual machine first.`;
	if (/in_use_by_container/.test(message))
		return `A container uses ${subject}. Remove it from the container first.`;
	if (/cannot_delete_critical|cannot_delete_container_image_layer/.test(message))
		return `Sylve needs ${subject}, so it can’t be deleted.`;
	if (/dataset_identity_changed|_not_found/.test(message))
		return 'The volumes changed while this dialog was open. Close it and try again.';
	if (/\bbusy\b/i.test(message)) return `Something still has ${subject} open.`;
	return message;
}

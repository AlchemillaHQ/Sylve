/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { DetailedDataset } from '$lib/types/zfs/dataset';
import {
	formatBytesBinary,
	normalizeSizeInputExact,
	parseSizeInputToBytes,
	toZfsBytesString
} from '$lib/utils/bytes';
import { isEncrypted } from '../FileSystems/filesystems';
import {
	INHERIT,
	localValue,
	needsPassphrase,
	readOnlyTunable,
	tunables,
	type Choice,
	type EditChanges,
	type Tunable
} from '../FileSystems/form';
import { blockSize, isSparse, volumeSize } from './volumes';

export type VolumeTunableKey =
	| 'compression'
	| 'checksum'
	| 'dedup'
	| 'primarycache'
	| 'volmode'
	| 'readonly';

export interface VolumeDraft {
	name: string;
	parent: string;
	size: string;
	provisioning: string;
	blockSize: string;
	encryption: string;
	passphrase: string;
	compression: string;
	checksum: string;
	dedup: string;
	primarycache: string;
	volmode: string;
	readonly: string;
}

function fileSystemTunable(key: 'compression' | 'checksum' | 'dedup'): Tunable<VolumeTunableKey> {
	return tunables.find((tunable) => tunable.key === key) as Tunable<VolumeTunableKey>;
}

export const compressionTunable = fileSystemTunable('compression');

export const volumeModeTunable: Tunable<VolumeTunableKey> = {
	key: 'volmode',
	label: 'Volume Mode',
	description:
		'How FreeBSD exposes the volume. Device Only gives a plain disk device, which suits virtual machines and iSCSI. GEOM also lets FreeBSD read the partitions on it. Hidden exposes no device at all. System Default follows this node’s vfs.zfs.vol.mode setting.',
	choices: [
		{ value: 'dev', label: 'Device Only' },
		{ value: 'geom', label: 'GEOM' },
		{ value: 'none', label: 'Hidden' },
		{ value: 'default', label: 'System Default' }
	]
};

export const advancedTunables: Tunable<VolumeTunableKey>[] = [
	fileSystemTunable('checksum'),
	fileSystemTunable('dedup'),
	{
		key: 'primarycache',
		label: 'Primary Cache',
		description:
			'What ZFS keeps in memory for this volume. Metadata Only suits virtual machine disks, whose guests cache their own data.',
		choices: [
			{ value: 'all', label: 'All' },
			{ value: 'metadata', label: 'Metadata Only' },
			{ value: 'none', label: 'None' }
		]
	}
];

export const volumeReadOnlyTunable: Tunable<VolumeTunableKey> = {
	...readOnlyTunable,
	key: 'readonly',
	description: 'Prevents any writes to this volume.'
};

const editableTunables = [
	compressionTunable,
	volumeModeTunable,
	...advancedTunables,
	volumeReadOnlyTunable
];

export const provisioningChoices: Choice[] = [
	{ value: 'sparse', label: 'Sparse' },
	{ value: 'thick', label: 'Thick' }
];

export const blockSizeChoices: Choice[] = [4096, 8192, 16384, 32768, 65536, 131072].map(
	(bytes) => ({
		value: String(bytes),
		label: bytes === 16384 ? `${formatBytesBinary(bytes)} (Default)` : formatBytesBinary(bytes)
	})
);

export function newVolumeDraft(parent: DetailedDataset | undefined): VolumeDraft {
	return {
		name: '',
		parent: parent?.name ?? '',
		size: '',
		provisioning: 'sparse',
		blockSize: '16384',
		encryption: parent && isEncrypted(parent) ? INHERIT : 'off',
		passphrase: '',
		compression: INHERIT,
		checksum: INHERIT,
		dedup: INHERIT,
		primarycache: INHERIT,
		volmode: 'dev',
		readonly: 'off'
	};
}

export function editVolumeDraft(volume: DetailedDataset): VolumeDraft {
	const draft: VolumeDraft = {
		name: volume.name,
		parent: volume.name.split('/').slice(0, -1).join('/'),
		size: normalizeSizeInputExact(volumeSize(volume)) ?? '',
		provisioning: isSparse(volume) ? 'sparse' : 'thick',
		blockSize: String(blockSize(volume) || ''),
		encryption: INHERIT,
		passphrase: '',
		compression: INHERIT,
		checksum: INHERIT,
		dedup: INHERIT,
		primarycache: INHERIT,
		volmode: INHERIT,
		readonly: INHERIT
	};
	for (const tunable of editableTunables) {
		draft[tunable.key] = localValue(volume, tunable.key) ?? INHERIT;
	}
	return draft;
}

export function sizeBytes(text: string): number | null {
	const value = text.trim();
	return value ? parseSizeInputToBytes(value) : null;
}

export function sizeProblem(
	text: string,
	limits: { blockSize: number; max?: number; current?: number; shrinkBlocked?: string }
): string {
	if (!text.trim()) return 'Enter a size';
	const bytes = sizeBytes(text);
	if (bytes === null) return 'Use a size like 512M or 10G';
	if (bytes <= 0) return 'Must be more than 0';
	if (limits.blockSize > 0 && bytes % limits.blockSize !== 0)
		return `Must be a multiple of the ${formatBytesBinary(limits.blockSize)} block size`;
	if (limits.current !== undefined && bytes < limits.current && limits.shrinkBlocked)
		return limits.shrinkBlocked;
	if (limits.max !== undefined && bytes > limits.max)
		return `Can’t be more than the ${formatBytesBinary(limits.max)} available`;
	return '';
}

export function createVolumeProperties(draft: VolumeDraft): Record<string, string> {
	const properties: Record<string, string> = {
		size: toZfsBytesString(sizeBytes(draft.size) ?? 0),
		sparse: draft.provisioning === 'sparse' ? 'on' : 'off',
		volblocksize: draft.blockSize
	};
	for (const tunable of [compressionTunable, volumeModeTunable, ...advancedTunables]) {
		if (draft[tunable.key] !== INHERIT) properties[tunable.key] = draft[tunable.key];
	}
	if (needsPassphrase(draft)) {
		properties.encryption = draft.encryption;
		properties.encryptionKey = draft.passphrase;
	}
	return properties;
}

export function editVolumeProperties(initial: VolumeDraft, draft: VolumeDraft): EditChanges {
	const properties: Record<string, string> = {};
	const inherit: string[] = [];
	const size = sizeBytes(draft.size);
	if (size !== null && size !== sizeBytes(initial.size))
		properties.volsize = toZfsBytesString(size);
	if (draft.provisioning !== initial.provisioning)
		properties.refreservation = draft.provisioning === 'thick' ? 'auto' : 'none';
	for (const tunable of editableTunables) {
		if (draft[tunable.key] === initial[tunable.key]) continue;
		if (draft[tunable.key] === INHERIT) inherit.push(tunable.key);
		else properties[tunable.key] = draft[tunable.key];
	}
	return { properties, inherit };
}

export function shrinks(initial: VolumeDraft, draft: VolumeDraft): boolean {
	const before = sizeBytes(initial.size);
	const after = sizeBytes(draft.size);
	return before !== null && after !== null && after < before;
}

export function volumeSaveErrorMessage(message: string): string {
	const iscsi = message.match(/_shared_over_iscsi_by_(\S+)/);
	if (iscsi)
		return `The iSCSI target ${iscsi[1]} shares this volume. Stop sharing it before shrinking the volume or changing its volume mode.`;
	if (/volume_\S+_in_use/.test(message)) return 'A running virtual machine is using this volume.';
	if (/replication_protected_dataset_mutation_blocked/.test(message))
		return 'Replication protects this volume, so it can’t be changed.';
	if (/must be a multiple of volume block size/i.test(message))
		return 'The size must be a multiple of the block size.';
	if (/out of space|insufficient|not enough space/i.test(message))
		return 'There isn’t enough free space in the pool.';
	if (/already exists/i.test(message))
		return 'A volume or file system with this name already exists.';
	if (/invalid_encryption_key_length/.test(message))
		return 'The passphrase must be 32 to 512 characters.';
	if (/encryption key (is )?not loaded|key not loaded|keystatus/i.test(message))
		return 'The parent’s encryption key is not loaded.';
	return message;
}

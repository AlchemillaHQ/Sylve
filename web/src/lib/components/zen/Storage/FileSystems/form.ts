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
import { createFSProps } from '$lib/utils/zfs/dataset/fs';
import { isEncrypted, isKeyUnavailable, propertyValue, quotaBytes } from './filesystems';

export const INHERIT = 'inherit';

export interface Choice {
	value: string;
	label: string;
}

export type TunableKey =
	| 'compression'
	| 'atime'
	| 'checksum'
	| 'dedup'
	| 'recordsize'
	| 'aclinherit'
	| 'aclmode'
	| 'readonly';

export interface Tunable<K extends string = TunableKey> {
	key: K;
	label: string;
	description: string;
	choices: Choice[];
}

const compressionNames: Record<string, string> = {
	on: 'On',
	off: 'Off',
	lz4: 'LZ4',
	zstd: 'Zstandard',
	'zstd-fast': 'Zstandard Fast',
	gzip: 'Gzip',
	lzjb: 'LZJB',
	zle: 'ZLE'
};

const checksumNames: Record<string, string> = {
	on: 'On',
	off: 'Off',
	fletcher2: 'Fletcher-2',
	fletcher4: 'Fletcher-4',
	sha256: 'SHA-256',
	sha512: 'SHA-512',
	skein: 'Skein',
	edonr: 'Edon-R',
	blake3: 'BLAKE3',
	noparity: 'No Parity'
};

export function compressionLabel(value: string): string {
	if (compressionNames[value]) return compressionNames[value];
	const level = value.match(/^(zstd-fast|zstd|gzip)-(\d+)$/);
	if (level) return `${compressionNames[level[1]]} (Level ${level[2]})`;
	return value.toUpperCase();
}

export function checksumLabel(value: string): string {
	return checksumNames[value] ?? value.toUpperCase();
}

export function encryptionLabel(value: string): string {
	if (value === 'off') return 'Off';
	if (value === 'on') return 'On (AES-256-GCM)';
	return value.toUpperCase();
}

export const tunables: Tunable[] = [
	{
		key: 'compression',
		label: 'Compression',
		description:
			'Compresses data as it is written. LZ4 and Zstandard are fast and save space for most data.',
		choices: Object.keys(compressionNames).map((value) => ({
			value,
			label: compressionLabel(value)
		}))
	},
	{
		key: 'atime',
		label: 'Access Time',
		description: 'Records when files are read. Turning it off avoids a write on every read.',
		choices: createFSProps.atime
	},
	{
		key: 'checksum',
		label: 'Checksum',
		description: 'How ZFS verifies data. Leave it on unless you need a specific algorithm.',
		choices: createFSProps.checksum.map((choice) => ({
			value: choice.value,
			label: checksumLabel(choice.value)
		}))
	},
	{
		key: 'dedup',
		label: 'Deduplication',
		description:
			'Stores identical blocks only once. It needs a lot of memory, so leave it off unless you know it helps.',
		choices: createFSProps.dedup
	},
	{
		key: 'recordsize',
		label: 'Record Size',
		description:
			'The largest block size for files. Smaller sizes suit databases, larger sizes suit big media files.',
		choices: [
			{ value: '8192', label: '8 KiB (PostgreSQL)' },
			{ value: '16384', label: '16 KiB (MySQL)' },
			{ value: '131072', label: '128 KiB (Default)' },
			{ value: '1048576', label: '1 MiB (Large Files)' }
		]
	},
	{
		key: 'aclinherit',
		label: 'ACL Inherit',
		description: 'How new files and directories inherit access control entries.',
		choices: createFSProps.aclInherit
	},
	{
		key: 'aclmode',
		label: 'ACL Mode',
		description: 'How access control lists change when permissions are changed with chmod.',
		choices: createFSProps.aclMode
	}
];

export const compressionTunable = tunables.find((tunable) => tunable.key === 'compression')!;
export const advancedTunables = tunables.filter((tunable) => tunable.key !== 'compression');

export const readOnlyTunable: Tunable = {
	key: 'readonly',
	label: 'Read Only',
	description: 'Prevents any changes to the files in this file system.',
	choices: [
		{ value: 'off', label: 'No' },
		{ value: 'on', label: 'Yes' }
	]
};

const editableTunables = [...tunables, readOnlyTunable];

export interface EditChanges {
	properties: Record<string, string>;
	inherit: string[];
}

export interface Draft {
	name: string;
	parent: string;
	quota: string;
	mountpoint: string;
	encryption: string;
	passphrase: string;
	readonly: string;
	compression: string;
	atime: string;
	checksum: string;
	dedup: string;
	recordsize: string;
	aclinherit: string;
	aclmode: string;
}

export function displayValue(key: string, value: string | undefined): string {
	if (value === undefined) return 'Not Reported';
	if (key === 'recordsize') {
		const bytes = Number(value);
		return Number.isFinite(bytes) && bytes > 0 ? formatBytesBinary(bytes) : value;
	}
	if (key === 'compression') return compressionLabel(value);
	if (key === 'checksum') return checksumLabel(value);
	const tunable = editableTunables.find((entry) => entry.key === key);
	return tunable?.choices.find((choice) => choice.value === value)?.label ?? value;
}

function sourceType(dataset: DetailedDataset, key: string): string | undefined {
	return dataset.propertyDetails?.[key]?.source?.type;
}

export function localValue(dataset: DetailedDataset, key: string): string | undefined {
	const value = propertyValue(dataset, key);
	return value && !['INHERITED', 'DEFAULT', 'RECEIVED'].includes(sourceType(dataset, key) ?? '')
		? value
		: undefined;
}

export function tunableChoices(
	tunable: Tunable<string>,
	mode: 'new' | 'edit',
	dataset: DetailedDataset | undefined,
	parent?: DetailedDataset
): Choice[] {
	const choices = [...tunable.choices];
	const label = (value: string) =>
		tunable.choices.find((choice) => choice.value === value)?.label ??
		displayValue(tunable.key, value);
	if (!dataset)
		return mode === 'new' ? [{ value: INHERIT, label: 'Inherit' }, ...choices] : choices;
	const value = propertyValue(dataset, tunable.key);
	if (value && !choices.some((choice) => choice.value === value)) {
		choices.push({ value, label: label(value) });
	}
	const shown = value ? label(value) : '';
	const suffix = shown ? ` (${shown})` : '';
	if (mode === 'new') return [{ value: INHERIT, label: `Inherit${suffix}` }, ...choices];
	const source = sourceType(dataset, tunable.key);
	if (source === 'INHERITED') {
		const from = dataset.propertyDetails?.[tunable.key]?.source?.data;
		const label =
			shown && from && from !== '-' ? `Inherited (${shown} from ${from})` : `Inherited${suffix}`;
		return [{ value: INHERIT, label }, ...choices];
	}
	if (source === 'DEFAULT') return [{ value: INHERIT, label: `Default${suffix}` }, ...choices];
	if (source === 'RECEIVED') return [{ value: INHERIT, label: `Received${suffix}` }, ...choices];
	if (source !== 'LOCAL') return choices;
	if (!parent) return [{ value: INHERIT, label: 'Reset to Default' }, ...choices];
	const inherited = propertyValue(parent, tunable.key);
	return [
		{
			value: INHERIT,
			label: inherited ? `Inherit (${label(inherited)})` : 'Inherit'
		},
		...choices
	];
}

export function encryptionChoices(parent: DetailedDataset | undefined): Choice[] {
	const algorithms = createFSProps.encryption
		.filter((choice) => choice.value !== 'off')
		.map((choice) => ({ value: choice.value, label: encryptionLabel(choice.value) }));
	if (parent && isEncrypted(parent)) {
		const algorithm = propertyValue(parent, 'encryption') ?? 'on';
		return [{ value: INHERIT, label: `Inherit (${encryptionLabel(algorithm)})` }, ...algorithms];
	}
	return [{ value: 'off', label: 'Off' }, ...algorithms];
}

export function newDraft(parent: DetailedDataset | undefined): Draft {
	return {
		name: '',
		parent: parent?.name ?? '',
		quota: '',
		mountpoint: '',
		encryption: parent && isEncrypted(parent) ? INHERIT : 'off',
		passphrase: '',
		readonly: 'off',
		compression: INHERIT,
		atime: INHERIT,
		checksum: INHERIT,
		dedup: INHERIT,
		recordsize: INHERIT,
		aclinherit: INHERIT,
		aclmode: INHERIT
	};
}

export function editDraft(dataset: DetailedDataset): Draft {
	const quota = quotaBytes(dataset);
	const draft: Draft = {
		name: dataset.name,
		parent: dataset.name.split('/').slice(0, -1).join('/'),
		quota: quota ? (normalizeSizeInputExact(quota) ?? '') : '',
		mountpoint: dataset.mountpoint.trim(),
		encryption: INHERIT,
		passphrase: '',
		readonly: INHERIT,
		compression: INHERIT,
		atime: INHERIT,
		checksum: INHERIT,
		dedup: INHERIT,
		recordsize: INHERIT,
		aclinherit: INHERIT,
		aclmode: INHERIT
	};
	for (const tunable of editableTunables) {
		const source = sourceType(dataset, tunable.key);
		const value = propertyValue(dataset, tunable.key);
		if (value && !['INHERITED', 'DEFAULT', 'RECEIVED'].includes(source ?? '')) {
			draft[tunable.key] = value;
		}
	}
	return draft;
}

export function nameProblem(name: string, parent: string, existing: Set<string>): string {
	const value = name.trim();
	if (!value) return 'Enter a name';
	if (!/^[a-zA-Z0-9_.-]+$/.test(value)) return 'Use letters, digits, and - _ or . only';
	if (value.startsWith('.') || value.startsWith('-')) return 'Can’t start with . or -';
	if (!parent) return '';
	if (`${parent}/${value}`.length > 255) return 'The full path must be at most 255 characters';
	if (!parent.includes('/') && value === 'sylve') return 'Reserved for Sylve';
	if (existing.has(`${parent}/${value}`)) return `Already exists in ${parent}`;
	return '';
}

export function parentProblem(parent: DetailedDataset | undefined): string {
	if (!parent) return 'Choose a parent';
	if (isKeyUnavailable(parent)) return 'Its encryption key is not loaded';
	return '';
}

export function quotaProblem(text: string, limits: { max?: number; min?: number }): string {
	const value = text.trim();
	if (!value) return '';
	const bytes = parseSizeInputToBytes(value);
	if (bytes === null) return 'Use a size like 512M or 10G';
	if (limits.max !== undefined && bytes > limits.max)
		return `Can’t be more than the ${formatBytesBinary(limits.max)} available`;
	if (limits.min !== undefined && bytes > 0 && bytes < limits.min)
		return `Must be at least ${formatBytesBinary(limits.min)}, the space already used`;
	return '';
}

export function mountpointProblem(text: string, required: boolean): string {
	const value = text.trim();
	if (!value) return required ? 'Enter a path, none or legacy' : '';
	if (value === 'none' || value === 'legacy') return '';
	if (!value.startsWith('/')) return 'Use an absolute path, none or legacy';
	return '';
}

export function needsPassphrase(draft: Pick<Draft, 'encryption'>): boolean {
	return draft.encryption !== 'off' && draft.encryption !== INHERIT;
}

export function passphraseProblem(draft: Pick<Draft, 'encryption' | 'passphrase'>): string {
	if (!needsPassphrase(draft)) return '';
	const length = new TextEncoder().encode(draft.passphrase).length;
	if (length === 0) return 'Enter or generate a passphrase';
	if (length < 32) return 'Must be at least 32 characters';
	if (length > 512) return 'Must be at most 512 characters';
	return '';
}

function quotaValue(text: string): number {
	return parseSizeInputToBytes(text.trim()) ?? 0;
}

export function createProperties(draft: Draft): Record<string, string> {
	const properties: Record<string, string> = {};
	const quota = quotaValue(draft.quota);
	if (quota > 0) properties.quota = toZfsBytesString(quota);
	if (draft.mountpoint.trim()) properties.mountpoint = draft.mountpoint.trim();
	for (const tunable of tunables) {
		if (draft[tunable.key] !== INHERIT) properties[tunable.key] = draft[tunable.key];
	}
	if (needsPassphrase(draft)) {
		properties.encryption = draft.encryption;
		properties.encryptionKey = draft.passphrase;
	}
	return properties;
}

export function editProperties(initial: Draft, draft: Draft): EditChanges {
	const properties: Record<string, string> = {};
	const inherit: string[] = [];
	const quota = quotaValue(draft.quota);
	if (quota !== quotaValue(initial.quota))
		properties.quota = quota > 0 ? toZfsBytesString(quota) : '0B';
	if (draft.mountpoint.trim() !== initial.mountpoint.trim())
		properties.mountpoint = draft.mountpoint.trim();
	for (const tunable of editableTunables) {
		if (draft[tunable.key] === initial[tunable.key]) continue;
		if (draft[tunable.key] === INHERIT) inherit.push(tunable.key);
		else properties[tunable.key] = draft[tunable.key];
	}
	return { properties, inherit };
}

export function hasEditChanges(changes: EditChanges): boolean {
	return Object.keys(changes.properties).length > 0 || changes.inherit.length > 0;
}

export function saveErrorMessage(message: string): string {
	if (/size is less than current used or reserved space/i.test(message))
		return 'The quota is smaller than the space this file system already uses.';
	if (/already exists/i.test(message)) return 'A file system with this name already exists.';
	if (/invalid_encryption_key_length/.test(message))
		return 'The passphrase must be 32 to 512 characters.';
	if (/managed_dataset_mountpoint_edit_not_supported/.test(message))
		return 'Sylve manages this file system’s mount point, so it can’t be changed here.';
	if (/download_storage_in_use/.test(message))
		return 'This file system holds download storage that is in use, so it can’t be changed right now.';
	if (/encryption key (is )?not loaded|key not loaded|keystatus/i.test(message))
		return 'The parent’s encryption key is not loaded.';
	return message;
}

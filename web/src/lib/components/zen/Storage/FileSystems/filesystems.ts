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
import { GZFSDatasetTypeSchema, type Dataset, type DetailedDataset } from '$lib/types/zfs/dataset';
import { formatBytesBinary } from '$lib/utils/bytes';

export interface FileSystemsData {
	hostname: string;
	datasets: DetailedDataset[] | APIResponse;
	settings?: BasicSettings;
	fresh?: boolean;
}

export interface FileSystemRow {
	dataset: DetailedDataset;
	order: number;
	depth: number;
	parent: string;
	leaf: string;
	children: number;
	descendants: number;
}

function compareNames(a: string, b: string): number {
	const left = a.split('/');
	const right = b.split('/');
	for (let index = 0; index < Math.min(left.length, right.length); index++) {
		const result = left[index].localeCompare(right[index], undefined, { numeric: true });
		if (result !== 0) return result;
	}
	return left.length - right.length;
}

export function fileSystemRows(datasets: DetailedDataset[]): FileSystemRow[] {
	const sorted = [...datasets].sort((a, b) => compareNames(a.name, b.name));
	const children = new Map<string, number>();
	const descendants = new Map<string, number>();
	for (const dataset of sorted) {
		const parts = dataset.name.split('/');
		for (let index = 1; index < parts.length; index++) {
			const ancestor = parts.slice(0, index).join('/');
			descendants.set(ancestor, (descendants.get(ancestor) ?? 0) + 1);
			if (index === parts.length - 1) children.set(ancestor, (children.get(ancestor) ?? 0) + 1);
		}
	}
	return sorted.map((dataset, order) => {
		const parts = dataset.name.split('/');
		return {
			dataset,
			order,
			depth: parts.length - 1,
			parent: parts.slice(0, -1).join('/'),
			leaf: parts.at(-1) ?? dataset.name,
			children: children.get(dataset.name) ?? 0,
			descendants: descendants.get(dataset.name) ?? 0
		};
	});
}

export function isExpandedByDefault(row: FileSystemRow): boolean {
	return row.depth === 0;
}

export function visibleRows(
	rows: FileSystemRow[],
	isExpanded: (row: FileSystemRow) => boolean
): FileSystemRow[] {
	const visible: FileSystemRow[] = [];
	let hidden: string | null = null;
	for (const row of rows) {
		if (hidden && row.dataset.name.startsWith(hidden)) continue;
		hidden = row.children > 0 && !isExpanded(row) ? `${row.dataset.name}/` : null;
		visible.push(row);
	}
	return visible;
}

export function isPoolRoot(dataset: Dataset): boolean {
	return dataset.name === dataset.pool;
}

export type PickedDataset = Pick<Dataset, 'name' | 'guid'> & Partial<Pick<Dataset, 'type'>>;

export function isVolume(dataset: PickedDataset | undefined): boolean {
	return dataset?.type === GZFSDatasetTypeSchema.enum.VOLUME;
}

export function isSylveDataset(name: string): boolean {
	const parts = name.split('/');
	return parts.length >= 2 && parts[1] === 'sylve';
}

export function isEncrypted(dataset: Dataset): boolean {
	return !['off', '-', 'none', ''].includes(dataset.properties?.encryption ?? 'off');
}

export function quotaBytes(dataset: Dataset): number | null {
	const value = Number(dataset.properties?.quota ?? 0);
	return Number.isFinite(value) && value > 0 ? value : null;
}

export function mountLabel(dataset: Dataset): string {
	const point = dataset.mountpoint.trim();
	if (!point || point === '-' || point === 'none') return 'Not Mounted';
	if (point === 'legacy') return 'Legacy';
	return point;
}

export function isMounted(dataset: Dataset): boolean {
	return dataset.properties?.mounted !== 'no' && mountLabel(dataset).startsWith('/');
}

export function propertyValue(dataset: DetailedDataset, name: string): string | undefined {
	const value =
		dataset.propertyDetails?.[name]?.value ??
		(dataset.properties as Record<string, string | undefined> | undefined)?.[name];
	return value === undefined || value === '-' ? undefined : value;
}

export function isKeyUnavailable(dataset: DetailedDataset): boolean {
	return isEncrypted(dataset) && propertyValue(dataset, 'keystatus') === 'unavailable';
}

export function switchLabel(value: string | undefined): string {
	if (value === undefined) return 'Not Reported';
	const labels: Record<string, string> = { on: 'On', off: 'Off', yes: 'Yes', no: 'No' };
	return labels[value] ?? value;
}

export function compressRatioLabel(dataset: DetailedDataset): string {
	const ratio = Number(propertyValue(dataset, 'compressratio'));
	return Number.isFinite(ratio) && ratio > 0 ? `${ratio.toFixed(2)}x` : 'Not Reported';
}

export function sizeLabel(value: string | undefined, zero = '0 B'): string {
	const bytes = Number(value);
	if (value === undefined || !Number.isFinite(bytes)) return 'Not Reported';
	return bytes > 0 ? formatBytesBinary(bytes) : zero;
}

const hiddenProperties = {
	filesystem: new Set(['name', 'type', 'volsize', 'volblocksize', 'volmode']),
	volume: new Set([
		'name',
		'type',
		'mountpoint',
		'mounted',
		'canmount',
		'quota',
		'recordsize',
		'atime',
		'acltype',
		'aclmode',
		'aclinherit',
		'sharenfs'
	])
};
const sizeProperties = new Set([
	'used',
	'available',
	'referenced',
	'logicalused',
	'usedbydataset',
	'written',
	'recordsize',
	'volsize',
	'volblocksize'
]);
const limitProperties = new Set(['quota', 'refreservation']);

export interface PropertyRow {
	name: string;
	value: string;
	source: string;
}

function sourceLabel(source: { type: string; data: string } | undefined): string {
	if (!source?.type) return 'Not Reported';
	switch (source.type) {
		case 'LOCAL':
			return 'Local';
		case 'DEFAULT':
			return 'Default';
		case 'INHERITED':
			return source.data && source.data !== '-' ? `Inherited from ${source.data}` : 'Inherited';
		case 'RECEIVED':
			return 'Received';
		case 'TEMPORARY':
			return 'Temporary';
		case 'NONE':
			return 'Not Applicable';
		default:
			return source.type.charAt(0) + source.type.slice(1).toLowerCase();
	}
}

function propertyLabel(name: string, value: string): string {
	if (value === '-' || value === '') return 'Not Applicable';
	if (sizeProperties.has(name)) return sizeLabel(value);
	if (limitProperties.has(name)) return sizeLabel(value, 'None');
	if (name === 'compressratio') {
		const ratio = Number(value);
		return Number.isFinite(ratio) ? `${ratio.toFixed(2)}x` : value;
	}
	return value;
}

export function propertyRows(
	dataset: DetailedDataset,
	kind: keyof typeof hiddenProperties = 'filesystem'
): PropertyRow[] {
	const details: Record<string, { value: string; source?: { type: string; data: string } }> =
		dataset.propertyDetails ??
		Object.fromEntries(
			Object.entries(dataset.properties ?? {})
				.filter((entry): entry is [string, string] => typeof entry[1] === 'string')
				.map(([name, value]) => [name, { value }])
		);
	return Object.entries(details)
		.filter(([name]) => !hiddenProperties[kind].has(name))
		.map(([name, detail]) => ({
			name,
			value: propertyLabel(name, detail.value),
			source: sourceLabel(detail.source)
		}));
}

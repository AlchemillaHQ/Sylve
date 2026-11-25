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
import type { Disk, Partition, SmartData, SmartNVMe } from '$lib/types/disk/disk';
import { formatBytesBinary } from '$lib/utils/bytes';
import { formatBigIntBytes, parseNVMeCounter, smartStatus } from '$lib/utils/disk';
import { getAPIErrorMessages } from '$lib/utils/http';

export interface DisksData {
	hostname: string;
	disks: Disk[] | APIResponse;
	fresh?: boolean;
}

export function diskIdentity(disk: Disk): string {
	return JSON.stringify([disk.device, disk.uuid, disk.serial, disk.model, disk.size]);
}

export function diskIcon(disk: Pick<Disk, 'type'>): string {
	switch (disk.type.toUpperCase()) {
		case 'HDD':
			return 'icon-[mdi--harddisk]';
		case 'SSD':
			return 'icon-[icon-park-outline--ssd]';
		case 'NVME':
			return 'icon-[sylve--nvme-vertical]';
		case 'VIRTUAL':
			return 'icon-[mdi--nas]';
		default:
			return 'icon-[lucide--hard-drive]';
	}
}

export function diskModel(disk: Disk): string {
	return (
		disk.model.trim() || (disk.device.startsWith('vtbd') ? 'VirtIO block device' : 'Not Reported')
	);
}

const bootPartition = (partition: Partition) => /boot|efi/i.test(partition.usage);

export function gptBlockReason(disk: Disk): string {
	if (disk.gpt) return 'This disk already has a GPT partition table.';
	if (disk.usage === 'ZFS') return 'This disk is in use by a ZFS pool.';
	if (disk.partitions.length > 0) return 'This disk already has a partition table. Clear it first.';
	return '';
}

export function clearBlockReason(disk: Disk): string {
	if (disk.usage === 'ZFS') return 'This disk is in use by a ZFS pool.';
	if (!disk.gpt && disk.partitions.length === 0) return 'This disk has no partition table.';
	if (disk.partitions.some(bootPartition)) {
		return 'Disks with a boot or EFI partition cannot be cleared.';
	}
	return '';
}

export function partitionBlockReason(partition: Partition): string {
	return bootPartition(partition) ? 'Boot and EFI partitions cannot be deleted.' : '';
}

export const MiB = 1024 * 1024;
export const maxPartitions = 128;
const partitionReserve = 128 * MiB;

export function partitionSpace(disk: Disk): number {
	const used = disk.partitions.reduce((total, partition) => total + partition.size, 0);
	const free = disk.size - used - partitionReserve;
	return free > 0 ? Math.floor(free / MiB) * MiB : 0;
}

export function createBlockReason(disk: Disk): string {
	if (disk.usage === 'ZFS') return 'This disk is in use by a ZFS pool.';
	if (!disk.gpt) {
		return disk.partitions.length > 0
			? 'Only GPT disks can be partitioned. Clear the partition table, then initialize GPT.'
			: 'This disk has no GPT partition table. Initialize GPT first.';
	}
	if (disk.partitions.some(bootPartition)) {
		return 'Disks with a boot or EFI partition cannot be modified.';
	}
	if (disk.partitions.length >= maxPartitions) return 'This disk has no free partition entries.';
	if (partitionSpace(disk) < MiB) return 'There is not enough free space on this disk.';
	return '';
}

export const partitionTypes = [
	{ value: 'freebsd-zfs', label: 'FreeBSD ZFS' },
	{ value: 'freebsd-ufs', label: 'FreeBSD UFS' },
	{ value: 'freebsd-swap', label: 'FreeBSD Swap' },
	{ value: 'linux-data', label: 'Linux Data' },
	{ value: 'linux-swap', label: 'Linux Swap' },
	{ value: 'ms-basic-data', label: 'Windows Basic Data' }
] as const;

export function diskActionError(response: APIResponse, fallback: string): string {
	const text = getAPIErrorMessages(response).join(', ');
	if (!text) return fallback;
	return `${text[0].toUpperCase()}${text.slice(1)}${/[.!?]$/.test(text) ? '' : '.'}`;
}

export function diskHealth(disk: Disk, sample?: Disk, reading = false): string {
	if (disk.type === 'Virtual') return 'Not Supported';
	if (!sample) return reading ? 'Reading…' : 'Not Read';
	return sample.smartData ? smartStatus(sample) : 'Unavailable';
}

export function wearoutPercent(sample?: Disk): number | null {
	const value = sample?.wearOut.trim() ?? '';
	return /^\d+(\.\d+)?$/.test(value) ? Number(value) : null;
}

export function wearoutApplies(disk: Disk): boolean {
	const type = disk.type.toUpperCase();
	return type !== 'HDD' && type !== 'VIRTUAL';
}

export function diskWearout(disk: Disk, sample?: Disk, reading = false): string {
	if (!wearoutApplies(disk)) return 'Not Applicable';
	if (!sample) return reading ? 'Reading…' : 'Not Read';
	const used = wearoutPercent(sample);
	return used === null ? 'Not Reported' : `${used}%`;
}

export type Attention = 'failing' | 'warning';

export function isNVMeSmart(data: SmartData | SmartNVMe): data is SmartNVMe {
	return 'criticalWarning' in data;
}

export function formatCount(exact: string, fallback: number): string {
	return (parseNVMeCounter(exact, fallback) ?? fallback).toLocaleString();
}

export function formatPowerOn(exact: string, hours: number): string {
	const total = Number(parseNVMeCounter(exact, hours) ?? hours);
	const text = `${formatCount(exact, hours)} h`;
	if (total >= 8760) return `${text} · ${(total / 8760).toFixed(1)} yrs`;
	if (total >= 48) return `${text} · ${Math.floor(total / 24)} days`;
	return text;
}

export interface AtaAttributeRow {
	key: string;
	id: number;
	name: string;
	value?: number;
	worst?: number;
	threshold?: number;
	raw: string;
	rawValue: number;
	attention?: Attention;
	reason?: string;
}

const ataPredictors: Record<number, string> = {
	5: 'Sectors have been reallocated',
	187: 'Uncorrectable errors were reported',
	188: 'Commands timed out',
	197: 'Sectors are pending reallocation',
	198: 'Sectors could not be corrected offline'
};

const attentionRank = (attention?: Attention) =>
	attention === 'failing' ? 0 : attention === 'warning' ? 1 : 2;

export function ataAttributeRows(data: SmartData): AtaAttributeRow[] {
	const ata = data.device.protocol.toUpperCase() === 'ATA';
	return sortByAttention(
		[...(data.attributes ?? [])]
			.sort((a, b) => a.page - b.page || a.id - b.id)
			.map((attribute) => {
				let attention: Attention | undefined;
				let reason: string | undefined;
				if (attribute.when_failed === 'FAILING_NOW') {
					attention = 'failing';
					reason = 'Failing now: at or below its threshold';
				} else if (attribute.when_failed) {
					attention = 'warning';
					reason =
						attribute.when_failed === 'In_the_past'
							? 'Reached its threshold in the past'
							: attribute.when_failed;
				} else if (ata && attribute.raw_value > 0 && ataPredictors[attribute.id]) {
					attention = 'warning';
					reason = ataPredictors[attribute.id];
				}
				return {
					key: `${attribute.page}:${attribute.id}`,
					id: attribute.id,
					name: attribute.name.replaceAll('_', ' ') || 'Unknown',
					value: attribute.value,
					worst: attribute.worst,
					threshold: attribute.thresh,
					raw: attribute.raw_string || attribute.raw_value.toLocaleString(),
					rawValue: attribute.raw_value,
					attention,
					reason
				};
			})
	);
}

function sortByAttention(rows: AtaAttributeRow[]): AtaAttributeRow[] {
	return rows.sort((a, b) => attentionRank(a.attention) - attentionRank(b.attention));
}

export interface HealthItem {
	label: string;
	value: string;
	attention?: Attention;
}

const nvmeWarnings: [keyof SmartNVMe['criticalWarningState'], string][] = [
	['availableSpare', 'Spare capacity low'],
	['temperature', 'Temperature out of range'],
	['deviceReliability', 'Reliability degraded'],
	['readOnly', 'Read-only mode'],
	['volatileMemoryBackup', 'Volatile memory backup failed']
];

export function nvmeHealthItems(data: SmartNVMe): HealthItem[] {
	const active = nvmeWarnings
		.filter(([key]) => data.criticalWarningState[key])
		.map(([, label]) => label);
	const bits = Number.parseInt(data.criticalWarning, 16);
	const raised = active.length > 0 || (Number.isFinite(bits) && bits !== 0);
	const mediaErrors =
		parseNVMeCounter(data.mediaErrorsExact, data.mediaErrors) ??
		BigInt(Math.trunc(data.mediaErrors) || 0);
	const bytes = (exact: string, units: number) => {
		const parsed = parseNVMeCounter(exact, units);
		return parsed === undefined
			? formatBytesBinary(units * 512000)
			: formatBigIntBytes(parsed * 512000n);
	};
	return [
		{
			label: 'Critical Warnings',
			value:
				active.length > 0
					? active.join(', ')
					: raised
						? `Raised (${data.criticalWarning})`
						: 'None',
			attention: raised ? 'failing' : undefined
		},
		{
			label: 'Available Spare',
			value: `${data.availableSpare}% (threshold ${data.availableSpareThreshold}%)`,
			attention: data.availableSpare < data.availableSpareThreshold ? 'failing' : undefined
		},
		{
			label: 'Media Errors',
			value: mediaErrors.toLocaleString(),
			attention: mediaErrors > 0n ? 'warning' : undefined
		},
		{
			label: 'Error Log Entries',
			value: formatCount(data.errorInfoLogEntriesExact, data.errorInfoLogEntries)
		},
		{
			label: 'Unsafe Shutdowns',
			value: formatCount(data.unsafeShutdownsExact, data.unsafeShutdowns)
		},
		{ label: 'Data Read', value: bytes(data.dataUnitsReadExact, data.dataUnitsRead) },
		{ label: 'Data Written', value: bytes(data.dataUnitsWrittenExact, data.dataUnitsWritten) },
		{
			label: 'Host Read Commands',
			value: formatCount(data.hostReadCommandsExact, data.hostReadCommands)
		},
		{
			label: 'Host Write Commands',
			value: formatCount(data.hostWriteCommandsExact, data.hostWriteCommands)
		},
		{
			label: 'Controller Busy Time',
			value: `${formatCount(data.controllerBusyTimeExact, data.controllerBusyTime)} min`
		},
		{
			label: 'Warning Temperature Time',
			value: `${data.warningCompositeTempTime.toLocaleString()} min`
		},
		{
			label: 'Critical Temperature Time',
			value: `${data.errorCompositeTempTime.toLocaleString()} min`
		},
		{
			label: 'Thermal Throttle Events (T1 / T2)',
			value: `${data.temperature1TransitionCnt.toLocaleString()} / ${data.temperature2TransitionCnt.toLocaleString()}`
		},
		{
			label: 'Thermal Throttle Time (T1 / T2)',
			value: `${data.totalTimeForTemperature1.toLocaleString()} s / ${data.totalTimeForTemperature2.toLocaleString()} s`
		}
	];
}

/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { Column, Row } from '$lib/types/components/tree-table';
import type { Disk, Partition, SmartAttribute, SmartData, SmartNVMe } from '$lib/types/disk/disk';
import type { Zpool, ZpoolVdev } from '$lib/types/zfs/pool';
import type { CellComponent } from 'tabulator-tables';
import { generateNumberFromString } from './numbers';
import { formatBytesBinary } from './bytes';
import { renderWithIcon } from './table';

function formatBigIntBytes(bytes: bigint): string {
	const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB', 'EiB'];
	let unitIndex = 0;
	let divisor = 1n;
	while (unitIndex < units.length - 1 && bytes >= divisor * 1024n) {
		divisor *= 1024n;
		unitIndex++;
	}
	if (unitIndex === 0) return `${bytes.toString()} B`;
	const hundredths = (bytes * 100n + divisor / 2n) / divisor;
	const whole = hundredths / 100n;
	const fraction = (hundredths % 100n).toString().padStart(2, '0').replace(/0+$/, '');
	return `${whole.toString()}${fraction ? `.${fraction}` : ''} ${units[unitIndex]}`;
}

function parseNVMeCounter(exact: string, fallback: number): bigint | undefined {
	let value = exact.trim();
	if (value === '' && Number.isFinite(fallback)) value = Math.trunc(fallback).toString();
	if (!/^\d+$/.test(value)) return undefined;
	return BigInt(value);
}

function formatNVMeCounter(exact: string, fallback: number): string {
	return parseNVMeCounter(exact, fallback)?.toString() ?? fallback.toString();
}

function formatNVMeDataUnits(exact: string, fallback: number): string {
	const units = parseNVMeCounter(exact, fallback);
	if (units === undefined) return `${fallback} (${formatBytesBinary(fallback * 512000)})`;
	return `${units.toString()} (${formatBigIntBytes(units * 512000n)})`;
}

function formatNVMeHours(exact: string, fallback: number): string {
	const hours = parseNVMeCounter(exact, fallback);
	if (hours === undefined) return `${fallback} (${Math.floor(fallback / 24)} days)`;
	return `${hours.toString()} (${(hours / 24n).toString()} days)`;
}

export function parseSMART(disk: Disk): SmartAttribute | SmartAttribute[] {
	if (disk.type === 'NVMe') {
		const data = disk.smartData as SmartNVMe;
		return {
			'Available Spare': data.availableSpare,
			'Available Spare Threshold': data.availableSpareThreshold,
			'Controller Busy Time': formatNVMeCounter(
				data.controllerBusyTimeExact,
				data.controllerBusyTime
			),
			'Critical Warning': data.criticalWarning,
			'Critical Warning State': {
				'Available Spare': data.criticalWarningState.availableSpare,
				'Device Reliability': data.criticalWarningState.deviceReliability,
				'Read Only': data.criticalWarningState.readOnly,
				Temperature: data.criticalWarningState.temperature,
				'Volatile Memory Backup': data.criticalWarningState.volatileMemoryBackup
			},
			'Data Units Read': formatNVMeDataUnits(data.dataUnitsReadExact, data.dataUnitsRead),
			'Data Units Written': formatNVMeDataUnits(
				data.dataUnitsWrittenExact,
				data.dataUnitsWritten
			),
			'Error Info Log Entries': formatNVMeCounter(
				data.errorInfoLogEntriesExact,
				data.errorInfoLogEntries
			),
			'Host Read Commands': formatNVMeCounter(data.hostReadCommandsExact, data.hostReadCommands),
			'Host Write Commands': formatNVMeCounter(
				data.hostWriteCommandsExact,
				data.hostWriteCommands
			),
			'Media Errors': formatNVMeCounter(data.mediaErrorsExact, data.mediaErrors),
			'Percentage Used': data.percentageUsed,
			'Power Cycles': formatNVMeCounter(data.power_cycle_count_exact, data.power_cycle_count),
			'Power On Hours (Days)': formatNVMeHours(data.power_on_hours_exact, data.power_on_hours),
			Temperature: data.temperature,
			'Temperature 1 Transition Count': data.temperature1TransitionCnt,
			'Temperature 2 Transition Count': data.temperature2TransitionCnt,
			'Total Time For Temperature 1': data.totalTimeForTemperature1,
			'Total Time For Temperature 2': data.totalTimeForTemperature2,
			'Unsafe Shutdowns': formatNVMeCounter(data.unsafeShutdownsExact, data.unsafeShutdowns),
			'Warning Composite Temp Time': data.warningCompositeTempTime
		};
	} else if (disk.type === 'HDD' || disk.type === 'SSD') {
		const data = disk.smartData as SmartData;
		const attributes: SmartAttribute[] = [];

		if (data.attributes && data.attributes.length > 0) {
			for (const element of data.attributes) {
				attributes.push({
					['ID']: element.id,
					['Name']: element.name || '-',
					['Value']: element.value ?? '-',
					['Worst']: element.worst ?? '-',
					['Threshold']: element.thresh ?? '-',
					['Raw Value']: element.raw_value ?? '-',
					['Raw String']: element.raw_string || '-'
				});
			}
		}

		if (attributes.length > 0) {
			return attributes;
		}
	} else if (disk.type === 'Virtual') {
		return {};
	}

	return {};
}

export function smartStatus(disk: Disk): string {
	if (disk.smartData) {
		if (Object.prototype.hasOwnProperty.call(disk.smartData, 'passed')) {
			const data = disk.smartData as SmartData;
			if (!data.health_known) {
				return 'Unknown';
			}
			if (data.passed) {
				return 'Passed';
			}
			return 'Failed';
		}

		if (Object.prototype.hasOwnProperty.call(disk.smartData, 'criticalWarning')) {
			if ((disk.smartData as SmartNVMe).criticalWarning !== '0x00') {
				return 'Failed';
			}

			return 'Passed';
		}
	}

	return '-';
}

export function diskSpaceAvailable(disk: Disk, required: number): boolean {
	if (disk.usage === 'Partitions') {
		const total = disk.size;
		const used = disk.partitions.reduce((acc, cur) => acc + cur.size, 0);
		return total - used >= required;
	}

	return disk.size >= required;
}

export function zpoolUseableDisks(disks: Disk[]): Disk[] {
	const useable: Disk[] = [];
	for (const disk of disks) {
		if (disk.usage === 'Partitions') {
			continue;
		}

		if (disk.usage === 'Unused' && disk.gpt === false) {
			useable.push(disk);
		}
	}

	return useable;
}

function collectUsedDeviceNames(
	vdevs?: Record<string, ZpoolVdev> | null,
	out = new Set<string>()
): Set<string> {
	if (!vdevs) return out;

	for (const vdev of Object.values(vdevs)) {
		if (vdev.path?.startsWith('/dev/')) {
			out.add(vdev.path.split('/').pop()!);
		}

		if (vdev.vdevs) {
			collectUsedDeviceNames(vdev.vdevs, out);
		}
	}

	return out;
}

export function zpoolUseablePartitions(disks: Disk[], pools: Zpool[]): Partition[] {
	const usable: Partition[] = [];
	const usedPartitionNames = new Set<string>();

	for (const pool of pools) {
		collectUsedDeviceNames(pool.vdevs, usedPartitionNames);
	}

	for (const disk of disks) {
		if (disk.usage !== 'Partitions') continue;
		if (disk.partitions.some((p) => p.usage === 'EFI')) continue;

		for (const partition of disk.partitions) {
			if (!usedPartitionNames.has(partition.name)) {
				usable.push(partition);
			}
		}
	}

	return usable;
}

export function getDiskSize(disk: Disk): string {
	if (disk.usage === 'Partitions') {
		return disk.partitions.reduce((acc, cur) => acc + cur.size, 0).toString();
	}

	return disk.size.toString();
}

export function stripDev(disk: string): string {
	return disk.replace(/^\/dev\//, '');
}

export function generateTableData(disks: Disk[]): { rows: Row[]; columns: Column[] } {
	const rows: Row[] = [];
	const columns: Column[] = [
		{
			field: 'device',
			title: 'Device',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				const disk = disks.find((d) => d.device === value);

				if (disk) {
					if (disk.type === 'HDD') {
						return renderWithIcon('mdi:harddisk', value);
					}

					if (disk.type === 'NVMe') {
						return renderWithIcon('bi:nvme', value, 'rotate-90');
					}

					if (disk.type === 'SSD') {
						return renderWithIcon('icon-park-outline:ssd', value);
					}

					if (disk.type === 'Virtual') {
						return renderWithIcon('mdi:nas', value);
					}
				}

				if (value.match(/p\d+$/)) {
					return renderWithIcon('ant-design:partition-outlined', value);
				}

				return value;
			}
		},
		{
			field: 'type',
			title: 'Type'
		},
		{
			field: 'usage',
			title: 'Usage'
		},
		{
			field: 'size',
			title: 'Size',
			formatter: (cell: CellComponent) => {
				return formatBytesBinary(cell.getValue());
			}
		},
		{
			field: 'gpt',
			title: 'GPT'
		},
		{
			field: 'model',
			title: 'Model'
		},
		{
			field: 'serial',
			title: 'Serial'
		},
		{
			field: 'smartStatus',
			title: 'S.M.A.R.T.',
			visible: false
		},
		{
			field: 'wearOut',
			title: 'Wearout',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				if (!isNaN(value)) {
					return `${value} %`;
				}

				return value;
			}
		}
	];

	for (const disk of disks) {
		if (disk.size <= 0) continue;
		const row: Row = {
			id: generateNumberFromString(disk.uuid),
			device: disk.device,
			type: disk.type,
			usage: disk.usage,
			size: disk.size,
			gpt: disk.gpt ? 'Yes' : 'No',
			model: disk.model,
			serial: disk.serial,
			smartStatus: smartStatus(disk),
			wearOut: disk.type === 'Virtual' ? '-' : disk.wearOut
		};

		if (disk.partitions && disk.partitions.length > 0) {
			row.children = [];

			for (const partition of disk.partitions) {
				const partitionRow: Row = {
					id: generateNumberFromString(partition.uuid),
					device: partition.name,
					type: partition.usage,
					usage: partition.usage,
					size: partition.size,
					gpt: disk.gpt ? 'Yes' : 'No',
					model: '-',
					serial: '-',
					smartData: '-',
					wearOut: '-'
				};

				row.children.push(partitionRow);
			}
		} else {
			row.children = [];
		}

		rows.push(row);
	}

	return {
		rows: rows,
		columns: columns
	};
}

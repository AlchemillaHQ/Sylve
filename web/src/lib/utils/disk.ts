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
import humanFormat from 'human-format';
import type { CellComponent } from 'tabulator-tables';
import { generateNumberFromString } from './numbers';
import { renderWithIcon } from './table';

export function parseSMART(disk: Disk): SmartAttribute | SmartAttribute[] {
    if (disk.type === 'NVMe') {
        return {
            'Available Spare': (disk.smartData as SmartNVMe).availableSpare,
            'Available Spare Threshold': (disk.smartData as SmartNVMe).availableSpareThreshold,
            'Controller Busy Time': (disk.smartData as SmartNVMe).controllerBusyTime,
            'Critical Warning': (disk.smartData as SmartNVMe).criticalWarning,
            'Critical Warning State': {
                'Available Spare': (disk.smartData as SmartNVMe).criticalWarningState.availableSpare,
                'Device Reliability': (disk.smartData as SmartNVMe).criticalWarningState.deviceReliability,
                'Read Only': (disk.smartData as SmartNVMe).criticalWarningState.readOnly,
                Temperature: (disk.smartData as SmartNVMe).criticalWarningState.temperature,
                'Volatile Memory Backup': (disk.smartData as SmartNVMe).criticalWarningState
                    .volatileMemoryBackup
            },
            'Data Units Read': (disk.smartData as SmartNVMe).dataUnitsRead,
            'Data Units Written': (disk.smartData as SmartNVMe).dataUnitsWritten,
            'Error Info Log Entries': (disk.smartData as SmartNVMe).errorInfoLogEntries,
            'Host Read Commands': (disk.smartData as SmartNVMe).hostReadCommands,
            'Host Write Commands': (disk.smartData as SmartNVMe).hostWriteCommands,
            'Media Errors': (disk.smartData as SmartNVMe).mediaErrors,
            'Percentage Used': (disk.smartData as SmartNVMe).percentageUsed,
            'Power Cycles': (disk.smartData as SmartNVMe).power_cycle_count,
            'Power On Hours': (disk.smartData as SmartNVMe).power_on_hours,
            Temperature: (disk.smartData as SmartNVMe).temperature,
            'Temperature 1 Transition Count': (disk.smartData as SmartNVMe).temperature1TransitionCnt,
            'Temperature 2 Transition Count': (disk.smartData as SmartNVMe).temperature2TransitionCnt,
            'Total Time For Temperature 1': (disk.smartData as SmartNVMe).totalTimeForTemperature1,
            'Total Time For Temperature 2': (disk.smartData as SmartNVMe).totalTimeForTemperature2,
            'Unsafe Shutdowns': (disk.smartData as SmartNVMe).unsafeShutdowns,
            'Warning Composite Temp Time': (disk.smartData as SmartNVMe).warningCompositeTempTime
        };
    } else if (disk.type === 'HDD' || disk.type === 'SSD') {
        const data = disk.smartData as SmartData;
        const attributes: SmartAttribute[] = [];

        if (data.attributes && data.attributes.length > 0) {
            for (const element of data.attributes) {
                attributes.push({
                    ['ID']: element.id,
                    ['Name']: element.name || '-',
                    ['Value']: element.value || '-',
                    ['Worst']: element.worst || '-',
                    ['Threshold']: element.thresh || '-',
                    ['Raw Value']: element.raw_value || '-',
                    ['Raw String']: element.raw_string || '-',
                })
            }
        }

        if (attributes.length > 0) {
            return attributes;
        }
    }

    return {};
}

export function smartStatus(disk: Disk): string {
    if (disk.smartData) {
        if (disk.smartData.hasOwnProperty('passed')) {
            if ((disk.smartData as SmartData).passed) {
                return 'Passed';
            }
            return 'Failed';
        }

        if (disk.smartData.hasOwnProperty('criticalWarning')) {
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

export function isPartitionInDisk(disks: Disk[], partition: Partition): Disk | null {
    for (const disk of disks) {
        if (disk.usage === 'Partitions') {
            for (const p of disk.partitions) {
                const raw = p.name.replace(/p\d+$/, '');
                if (disk.device === raw) {
                    return disk;
                }
            }
        }
    }

    return null;
}

export function zpoolUseableDisks(disks: Disk[], pools: Zpool[]): Disk[] {
    const useable: Disk[] = [];
    for (const disk of disks) {
        if (disk.usage === 'Partitions') {
            continue;
        }

        if (disk.usage === 'Unused' && disk.gpt === false) {
            useable.push(disk);
        }
    }

    console.log('Useable disks:', useable);

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
                const row = cell.getRow();
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
                return humanFormat(cell.getValue());
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
            wearOut: disk.wearOut
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

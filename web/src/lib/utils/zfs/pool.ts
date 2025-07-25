import type { APIResponse, PieChartData, SeriesDataWithBaseline } from '$lib/types/common';
import type { Column, Row } from '$lib/types/components/tree-table';
import type { Disk } from '$lib/types/disk/disk';
import type { Dataset } from '$lib/types/zfs/dataset';
import type { Zpool } from '$lib/types/zfs/pool';
import humanFormat from 'human-format';
import { generateNumberFromString } from '../numbers';
import { renderWithIcon, sizeFormatter } from '../table';

export const raidTypeArr = [
    {
        value: 'stripe',
        label: 'Stripe',
        available: true
    },
    {
        value: 'mirror',
        label: 'Mirror',
        available: false
    },
    {
        value: 'raidz',
        label: 'RAIDZ',
        available: false
    },
    {
        value: 'raidz2',
        label: 'RAIDZ2',
        available: false
    },
    {
        value: 'raidz3',
        label: 'RAIDZ3',
        available: false
    }
];

export function generateTableData(
    pools: Zpool[],
    disks: Disk[]
): {
    rows: Row[];
    columns: Column[];
} {
    let rows: Row[] = [];
    let columns: Column[] = [
        {
            field: 'id',
            title: 'ID',
            visible: false
        },
        {
            field: 'name',
            title: 'Name',
            formatter: (cell) => {
                const value = cell.getValue();

                if (isPool(pools, value)) {
                    return renderWithIcon('bi:hdd-stack-fill', value);
                }

                if (value.match(/p\d+$/)) {
                    return renderWithIcon('ant-design:partition-outlined', value);
                }

                if (value.startsWith('/dev/')) {
                    const nameOnly = value.replace('/dev/', '');
                    const disk = disks.find((disk) => disk.device === nameOnly);
                    if (disk) {
                        if (disk.type === 'HDD') {
                            return renderWithIcon('mdi:harddisk', value);
                        } else if (disk.type === 'SSD') {
                            return renderWithIcon('icon-park-outline:ssd', value);
                        } else if (disk.type === 'NVMe') {
                            return renderWithIcon('bi:nvme', value, 'rotate-90');
                        }
                    }
                }

                return `<span class="whitespace-nowrap">${value}</span>`;
            }
        },
        {
            field: 'size',
            title: 'Size',
            formatter: sizeFormatter
        },
        {
            field: 'used',
            title: 'Used',
            formatter: sizeFormatter
        },
        {
            field: 'health',
            title: 'Health'
        },
        {
            field: 'redundancy',
            title: 'Redundancy'
        },
        {
            field: 'guid',
            title: 'GUID',
            visible: false
        }
    ];

    for (const pool of pools) {
        const poolRow = {
            id: generateNumberFromString(pool.name + '-pool'),
            name: pool.name,
            size: pool.size,
            used: pool.allocated,
            health: pool.health,
            redundancy: '',
            children: [] as Row[],
            guid: pool.guid || ''
        };

        for (const vdev of pool.vdevs) {
            if (vdev.name.includes('mirror') || vdev.name.includes('raid') || vdev.devices.length > 1) {
                let redundancy = 'Stripe';
                let vdevLabel = vdev.name;

                if (vdev.name.startsWith('mirror')) {
                    redundancy = 'Mirror';
                    vdevLabel = vdev.name.replace(/mirror-?(\d+)/i, 'Mirror $1');
                } else if (vdev.name.startsWith('raidz')) {
                    redundancy = 'RAIDZ ' + vdev.name.match(/raidz-?(\d+)/i)?.[1];
                    vdevLabel = vdev.name.replace(/^raidz/i, 'RAIDZ');
                }

                const vdevRow = {
                    id: generateNumberFromString(vdev.name),
                    name: vdevLabel,
                    size: vdev.alloc + vdev.free,
                    used: vdev.alloc,
                    health: vdev.health,
                    redundancy: '-',
                    children: [] as Row[]
                };

                for (const device of vdev.devices) {
                    if (
                        vdev.replacingDevices &&
                        vdev.replacingDevices.some(
                            (r) => r.oldDrive.name === device.name || r.newDrive.name === device.name
                        )
                    ) {
                        continue;
                    }

                    vdevRow.children.push({
                        id: generateNumberFromString(device.name),
                        name: device.name,
                        size: device.size,
                        used: '-',
                        health: device.health,
                        redundancy: '-',
                        children: []
                    });
                }

                if (vdev.replacingDevices && vdev.replacingDevices.length > 0) {
                    for (const replacing of vdev.replacingDevices) {
                        vdevRow.children.push({
                            id: generateNumberFromString(replacing.oldDrive.name),
                            name: `${replacing.oldDrive.name} [OLD]`,
                            size: replacing.oldDrive.size,
                            used: '-',
                            health: `${replacing.oldDrive.health} (Being replaced)`,
                            redundancy: '-',
                            children: []
                        });

                        vdevRow.children.push({
                            id: generateNumberFromString(replacing.newDrive.name),
                            name: `${replacing.newDrive.name} [NEW]`,
                            size: replacing.newDrive.size,
                            used: '-',
                            health: `${replacing.newDrive.health} (Replacement)`,
                            redundancy: '-',
                            children: []
                        });
                    }
                }

                poolRow.children.push(vdevRow);
                poolRow.redundancy = redundancy;
            } else {
                poolRow.children.push({
                    id: generateNumberFromString(vdev.devices[0].name),
                    name: vdev.devices[0].name,
                    size: vdev.devices[0].size,
                    used: '-',
                    health: vdev.devices[0].health,
                    redundancy: '-',
                    children: []
                });
                poolRow.redundancy = 'Stripe';
            }
        }

        rows.push(poolRow);

        if (pool.spares && pool.spares.length > 0) {
            const sparesRow: Row = {
                id: generateNumberFromString(`${pool.name}-spares`),
                name: 'Spares',
                size:
                    pool.spares.reduce((acc, spare) => acc + spare.size, 0) > 0
                        ? pool.spares.reduce((acc, spare) => acc + spare.size, 0)
                        : '-',
                used: '-',
                health: '-',
                redundancy: '-',
                children: []
            };

            for (const spare of pool.spares) {
                sparesRow.children!.push({
                    id: generateNumberFromString(spare.name),
                    name: spare.name,
                    size: spare.size,
                    used: '-',
                    health: spare.health,
                    redundancy: '-',
                    children: []
                });
            }

            poolRow.children!.push(sparesRow);
        }
    }

    // spares should be at the end of the pool
    rows = rows.map((row) => {
        if (row.children) {
            const sparesIndex = row.children.findIndex((child) => child.name === 'Spares');
            if (sparesIndex !== -1) {
                const sparesRow = row.children.splice(sparesIndex, 1)[0];
                row.children.push(sparesRow);
            }
        }
        return row;
    });

    return {
        rows,
        columns
    };
}

export function isPool(pools: Zpool[], name: string): boolean {
    return pools.some((pool) => pool.name === name);
}

export function isReplaceableDevice(pools: Zpool[], name: string): boolean {
    for (const pool of pools) {
        if (pool.vdevs.some((vdev) => vdev.name === name)) {
            return false; // False if we're striped
        }
    }

    return pools.some((pool) => {
        for (const vdev of pool.vdevs) {
            if (vdev.devices.some((device) => device.name === name)) {
                return true;
            }
        }
        return false;
    });
}

export function getPoolByDevice(pools: Zpool[], name: string): string {
    for (const pool of pools) {
        for (const vdev of pool.vdevs) {
            if (vdev.devices.some((device) => device.name === name)) {
                return pool.name;
            }
        }
    }

    return '';
}

export function parsePoolActionError(error: APIResponse): string {
    if (error.message && error.message === 'pool_create_failed') {
        if (error.error) {
            if (error.error.includes('mirror contains devices of different sizes')) {
                return 'Pool contains a mirror with devices of different sizes';
            } else if (error.error.includes('raidz contains devices of different sizes')) {
                return 'Pool contains a RAIDZ vdev with devices of different sizes';
            }
        }
    }

    if (error.message && error.message === 'pool_delete_failed') {
        if (error.error) {
            if (error.error.includes('pool or dataset is busy')) {
                return 'Pool is busy';
            }
        }
    }

    if (error.message && error.message === 'pool_edit_failed') {
        return 'Pool edit failed';
    }

    return '';
}

export function getPoolUsagePieData(pools: Zpool[], pool: string): PieChartData[] {
    const poolData = pools.find((p) => p.name === pool);

    return [
        {
            label: 'Used',
            value: poolData?.allocated || 0,
            color: 'chart-1'
        },
        {
            label: 'Free',
            value: poolData?.free || 0,
            color: 'chart-2'
        }
    ];
}

export function getDatasetCompressionHist(
    pool: string,
    datasets: Dataset[]
): SeriesDataWithBaseline[] {
    const results: SeriesDataWithBaseline[] = [];
    const related = datasets.filter(
        (dataset) => dataset.name.startsWith(pool + '/') || dataset.name === pool
    );

    for (const dataset of related) {
        const used = dataset.used || dataset.properties?.used;
        const logicalUsed = dataset.logicalused || dataset.properties?.logicalused;

        if (typeof used === 'number' && typeof logicalUsed === 'number' && logicalUsed > 0) {
            if (dataset.name.includes('/')) {
                results.push({
                    name: dataset.name,
                    baseline: logicalUsed,
                    value: used
                });
            }
        }
    }

    results.sort((a, b) => {
        if (a.baseline !== b.baseline) {
            return b.baseline - a.baseline;
        }

        const ratioA = a.value / a.baseline;
        const ratioB = b.value / b.baseline;

        return ratioB - ratioA;
    });

    return results;
}

export type StatType = 'allocated' | 'free' | 'size' | 'dedupRatio';

export function getPoolStatsCombined(poolStats: Record<string, any[]>, statType: StatType) {
    if (!poolStats) {
        return {
            poolStatsData: [],
            poolStatsKeys: []
        };
    }

    const poolStatsData = Object.entries(poolStats)
        .map(([poolName, stats]) => {
            if (!Array.isArray(stats)) return [];

            return stats.map((entry) => ({
                date: new Date(entry.time),
                [poolName]: entry[statType] ?? 0
            }));
        })
        .filter((array) => array.length > 0)
        // Sort each pool's data points by time
        .map((dataPoints) => dataPoints.sort((a, b) => a.date.getTime() - b.date.getTime()));

    const poolStatsKeys = Object.keys(poolStats).map((poolName, index) => {
        const colorId = `chart-${(index % 5) + 1}`; // cycle through chart1 → chart5
        return {
            key: poolName,
            title: poolName.charAt(0).toUpperCase() + poolName.slice(1),
            color: colorId
        };
    });

    return { poolStatsData, poolStatsKeys };
}

export const getDateFormatByInterval = (intervalValue: number, includeTime: boolean): string => {
    if (intervalValue <= 1) return includeTime ? 'hh:mm:ss a' : 'hh:mm a';
    if (intervalValue <= 5) return includeTime ? 'hh:mm a' : 'hh:mm a';
    if (intervalValue <= 60) return includeTime ? 'hh:mm a' : 'hh:mm a';
    if (intervalValue <= 1440) return includeTime ? 'MMM d, hh:mm a' : 'MMM d';
    if (intervalValue <= 10080) return includeTime ? 'MMM d, hh:mm a' : 'MMM d';
    if (intervalValue <= 40320) return includeTime ? 'MMM yyyy' : 'MMM yyyy';
    return includeTime ? 'yyyy' : 'yyyy';
};

export function formatValue(
    value: number,
    unformattedKeys: string[] | undefined,
    valueType: string
): string | number {
    if (unformattedKeys?.includes('dedupRatio')) return value;

    switch (valueType) {
        case 'fileSize':
            return humanFormat(value);
        case 'percentage':
            return `${value}%`;
        case 'celcius':
            return `${value}°C`;
        default:
            return value;
    }
}


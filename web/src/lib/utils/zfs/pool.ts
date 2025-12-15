import type { APIResponse, PieChartData, SeriesDataWithBaseline } from '$lib/types/common';
import type { Column, Row } from '$lib/types/components/tree-table';
import type { Disk } from '$lib/types/disk/disk';
import type { Dataset } from '$lib/types/zfs/dataset';
import type { ScanSentenceResult, ScanStatsRaw, Zpool, ZpoolStatusPool } from '$lib/types/zfs/pool';
import humanFormat from 'human-format';
import { generateNumberFromString } from '../numbers';
import { renderWithIcon, sizeFormatter } from '../table';
import { countKeys } from '../obj';
import { epochToLocal } from '../time';

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

function getPoolRedundancy(pool: Zpool): 'Stripe' | 'Mirror' | 'RAIDZ' | 'RAIDZ2' | 'RAIDZ3' {
	if (!pool.vdevs) {
		return 'Stripe';
	}

	for (const vdevKey in pool.vdevs) {
		const vdev = pool.vdevs[vdevKey];
		const vdevType = vdev.vdev_type.toLowerCase();

		if (vdevType === 'mirror') {
			return 'Mirror';
		} else if (vdevType === 'raidz') {
			if (vdev.name.startsWith('raidz1')) {
				return 'RAIDZ';
			} else if (vdev.name.startsWith('raidz2')) {
				return 'RAIDZ2';
			} else if (vdev.name.startsWith('raidz3')) {
				return 'RAIDZ3';
			} else {
				return 'RAIDZ';
			}
		}
	}

	return 'Stripe';
}

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
			id: generateNumberFromString(pool.name + pool.guid + '-pool'),
			name: pool.name,
			size: pool.size,
			used: pool.allocated,
			health: pool.state,
			redundancy: getPoolRedundancy(pool),
			children: [] as Row[],
			guid: pool.guid || ''
		};

		if (pool.vdevs && countKeys(pool.vdevs) > 0) {
			for (const vdev in pool.vdevs) {
				const current = pool.vdevs[vdev];
				if (
					current.vdev_type.toLowerCase() === 'raidz' ||
					current.vdev_type.toLowerCase() === 'mirror'
				) {
					const vdevRow: Row = {
						id: generateNumberFromString(current.name),
						name: current.name,
						size: current.size,
						used: current.allocated,
						health: current.state,
						redundancy: '-',
						children: []
					};

					for (const child in current.vdevs) {
						const childVdev = current.vdevs[child];
						vdevRow.children!.push({
							id: generateNumberFromString(childVdev.name),
							name: childVdev.name,
							size: childVdev.size,
							used: childVdev.allocated,
							health: childVdev.state,
							redundancy: '-',
							children: []
						});
					}

					poolRow.children!.push(vdevRow);
				} else {
					poolRow.children!.push({
						id: generateNumberFromString(current.name),
						name: current.name,
						size: current.size,
						used: current.allocated,
						health: current.state,
						redundancy: '-',
						children: []
					});
				}
			}
		}

		if (pool.l2cache && countKeys(pool.l2cache) > 0) {
			const l2cacheRow: Row = {
				id: generateNumberFromString(`${pool.name}-${pool.guid}-l2cache`),
				name: 'L2 Cache',
				size:
					Object.values(pool.l2cache).reduce((acc, cache) => acc + cache.size, 0) > 0
						? Object.values(pool.l2cache).reduce((acc, cache) => acc + cache.size, 0)
						: '-',
				used: '-',
				health: '-',
				redundancy: '-',
				children: []
			};

			for (const cache in pool.l2cache) {
				const current = pool.l2cache[cache];
				l2cacheRow.children!.push({
					id: generateNumberFromString(current.guid),
					name: current.name,
					size: current.size,
					used: '-',
					health: current.state,
					redundancy: '-',
					children: []
				});
			}

			poolRow.children!.push(l2cacheRow);
		}

		if (pool.logs && countKeys(pool.logs) > 0) {
			const logsRow: Row = {
				id: generateNumberFromString(`${pool.name}-${pool.guid}-logs`),
				name: 'Logs',
				size:
					Object.values(pool.logs).reduce((acc, log) => acc + log.size, 0) > 0
						? Object.values(pool.logs).reduce((acc, log) => acc + log.size, 0)
						: '-',
				used: '-',
				health: '-',
				redundancy: '-',
				children: []
			};

			for (const log in pool.logs) {
				const current = pool.logs[log];
				logsRow.children!.push({
					id: generateNumberFromString(current.guid),
					name: current.name,
					size: current.size,
					used: '-',
					health: current.state,
					redundancy: '-',
					children: []
				});
			}

			poolRow.children!.push(logsRow);
		}

		if (pool.spares && countKeys(pool.spares) > 0) {
			const sparesRow: Row = {
				id: generateNumberFromString(`${pool.name}-${pool.guid}-spares`),
				name: 'Spares',
				size:
					Object.values(pool.spares).reduce((acc, spare) => acc + spare.size, 0) > 0
						? Object.values(pool.spares).reduce((acc, spare) => acc + spare.size, 0)
						: '-',
				used: '-',
				health: '-',
				redundancy: '-',
				children: []
			};

			for (const spare in pool.spares) {
				const current = pool.spares[spare];
				sparesRow.children!.push({
					id: generateNumberFromString(current.guid),
					name: current.name,
					size: current.size,
					used: '-',
					health: current.state,
					redundancy: '-',
					children: []
				});
			}

			poolRow.children!.push(sparesRow);
		}

		rows.push(poolRow);
	}

	rows = rows.map((row) => {
		if (row.children) {
			const specialVdevs = ['Logs', 'L2 Cache', 'Spares'];
			specialVdevs.forEach((vdevName) => {
				const vdevIndex = row.children!.findIndex((child) => child.name === vdevName);
				if (vdevIndex !== -1) {
					const vdevRow = row.children!.splice(vdevIndex, 1)[0];
					row.children!.push(vdevRow);
				}
			});
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
		// if (pool.vdevs.some((vdev) => vdev.name === name)) {
		// 	return false; // False if we're striped
		// }
	}

	return pools.some((pool) => {
		// for (const vdev of pool.vdevs) {
		// 	if (vdev.devices.some((device) => device.name === name)) {
		// 		return true;
		// 	}
		// }
		return false;
	});
}

export function getPoolByDevice(pools: Zpool[], name: string): string {
	for (const pool of pools) {
		// for (const vdev of pool.vdevs) {
		// 	if (vdev.devices.some((device) => device.name === name)) {
		// 		return pool.name;
		// 	}
		// }
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
		const used = dataset.used;
		const logicalUsed = dataset.logicalused;

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

type ScanHandler = (stats: ZpoolStatusPool['scan_stats']) => ScanSentenceResult;

export function parseScanStats(stats: ZpoolStatusPool['scan_stats']): ScanSentenceResult {
	if (!stats || !stats.function) return { title: '', text: null, progressPercent: null };

	// safe number parser
	const num = (v: string | number | undefined): number => (v === undefined ? 0 : Number(v) || 0);
	const start = num(stats.start_time);
	const end = num(stats.end_time);
	const examined = num(stats.examined);
	const toExamine = num(stats.to_examine);
	const issued = num(stats.issued);
	const errors = num(stats.errors);
	const skipped = num(stats.skipped);
	const processed = num(stats.processed);
	const bytesPerScan = num(stats.bytes_per_scan);
	const issuedBytesPerScan = num(stats.issued_bytes_per_scan);

	const elapsed = Math.max(1, Date.now() / 1000 - start); // seconds
	const issuedPerSec = issued > 0 ? issued / elapsed : 0;

	const formatRemaining = (secs: number) => {
		if (secs <= 0) return '0s remaining';
		if (secs >= 3600)
			return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m remaining`;
		if (secs >= 60) return `${Math.floor(secs / 60)}m ${Math.floor(secs % 60)}s remaining`;
		return `${Math.floor(secs)}s remaining`;
	};

	// helpers for human strings
	const h = (v: number) => humanFormat(v);
	const scanRateStr = issuedPerSec > 0 ? `${humanFormat(Math.floor(issuedPerSec))}/s` : null;

	// handlers registry: add new functions here (resilver, trim...) as needed
	const handlers: Record<string, ScanHandler> = {
		scrub: (s) => {
			const progress = toExamine > 0 ? Math.min(100, Math.round((issued / toExamine) * 100)) : null;

			if (s?.state === 'SCANNING') {
				let timeRemaining = '';
				if (issuedPerSec > 0 && toExamine > issued) {
					timeRemaining = formatRemaining((toExamine - issued) / issuedPerSec);
				}

				let text = `Scrub in progress since ${epochToLocal(start)}: ${h(examined)} / ${h(toExamine)} scanned`;
				if (scanRateStr) text += `, ${h(issued)} / ${h(toExamine)} issued at ${scanRateStr}`;
				text += `, ${h(errors)} repaired`;
				if (progress !== null) text += `, ${progress}% done`;
				if (timeRemaining) text += `, ${timeRemaining}`;

				return { title: 'Pool Scrub', text, progressPercent: progress };
			}

			// finished / paused / canceled: show final summary (use end_time if available)
			const durationSec = end > start ? end - start : Math.max(0, Date.now() / 1000 - start);
			const durationText = (() => {
				if (durationSec >= 3600)
					return `${Math.floor(durationSec / 3600)}h ${Math.floor((durationSec % 3600) / 60)}m`;
				if (durationSec >= 60)
					return `${Math.floor(durationSec / 60)}m ${Math.floor(durationSec % 60)}s`;
				return `${Math.floor(durationSec)}s`;
			})();

			const text = `Scrub finished (${epochToLocal(end || start)}). ${h(examined)} / ${h(toExamine)} scanned, ${h(errors)} repaired, took ${durationText}`;
			return { title: 'Pool Scrub', text, progressPercent: progress ?? 100 };
		}

		// placeholder for future handlers:
		// resilver: s => { ... },
		// trim: s => { ... },
	};

	const fn = String(stats.function || '').toLowerCase();
	// pick handler by keyword (startsWith / includes) so "scrub" and "scrub-thing" both map
	for (const key of Object.keys(handlers)) {
		if (fn.includes(key)) return handlers[key](stats);
	}

	// default fallback: return minimal info
	return {
		title: stats.function,
		text: stats.state ? `${stats.function} ${stats.state.toLowerCase()}` : stats.function,
		progressPercent: null
	};
}

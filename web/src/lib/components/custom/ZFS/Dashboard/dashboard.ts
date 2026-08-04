import type {
	PoolStatPoint,
	ZFSDashboardPoolSeries,
	ZFSDashboardPoolSnapshot
} from '$lib/types/zfs/pool';
import { formatBytesBinary } from '$lib/utils/bytes';

export type RangeKey = '1h' | '24h' | '7d' | '30d' | '70d';
export type Tone = 'neutral' | 'success' | 'warning' | 'danger';

export const ranges: Array<{ value: RangeKey; label: string; seconds: number }> = [
	{ value: '1h', label: '1H', seconds: 60 * 60 },
	{ value: '24h', label: '24H', seconds: 24 * 60 * 60 },
	{ value: '7d', label: '7D', seconds: 7 * 24 * 60 * 60 },
	{ value: '30d', label: '30D', seconds: 30 * 24 * 60 * 60 },
	{ value: '70d', label: '70D', seconds: 70 * 24 * 60 * 60 }
];

export interface PoolSummary {
	totalSize: number;
	allocated: number;
	free: number;
	usedPercent: number;
	health: string;
	online: number;
	errors: number;
	statusUnavailable: number;
	fragmentation: number;
	dedupRatio: number;
	dataVdevs: number;
	disks: number;
}

export interface IOSummary {
	valid: boolean;
	readIOPS: number;
	writeIOPS: number;
	readBytesPerSecond: number;
	writeBytesPerSecond: number;
	averageLatency: number | null;
	intervalSeconds: number;
}

export interface VerificationSummary {
	value: string;
	detail: string;
	footer: string;
	tone: Tone;
	progress: number | null;
}

export function healthRank(health: string): number {
	switch (health.toUpperCase()) {
		case 'ONLINE':
			return 1;
		case 'DEGRADED':
			return 3;
		case 'OFFLINE':
		case 'REMOVED':
			return 4;
		case 'FAULTED':
		case 'UNAVAIL':
		case 'UNAVAILABLE':
		case 'SUSPENDED':
		case 'CORRUPT_DATA':
			return 5;
		default:
			return 2;
	}
}

function worseHealth(left: string, right: string): string {
	return healthRank(right) > healthRank(left) ? right : left;
}

function poolErrorCount(pool: ZFSDashboardPoolSnapshot): number {
	return pool.errors.read + pool.errors.write + pool.errors.checksum + pool.errors.scan;
}

export function summarizePools(values: ZFSDashboardPoolSnapshot[]): PoolSummary {
	let totalSize = 0;
	let allocated = 0;
	let free = 0;
	let online = 0;
	let errors = 0;
	let statusUnavailable = 0;
	let fragmentationWeighted = 0;
	let dedupWeighted = 0;
	let dedupWeight = 0;
	let dataVdevs = 0;
	let disks = 0;
	let health = values.length > 0 ? 'ONLINE' : 'NO POOLS';

	for (const pool of values) {
		totalSize += pool.size;
		allocated += pool.allocated;
		free += pool.free;
		if (pool.state.toUpperCase() === 'ONLINE') online++;
		health = worseHealth(health, pool.state);
		errors += poolErrorCount(pool);
		if (!pool.statusAvailable) statusUnavailable++;
		fragmentationWeighted += pool.fragmentation * pool.size;
		const poolDedupWeight = pool.allocated > 0 ? pool.allocated : pool.size;
		dedupWeighted += pool.dedupRatio * poolDedupWeight;
		dedupWeight += poolDedupWeight;
		dataVdevs += pool.topology.dataVdevs;
		disks += pool.topology.disks;
	}

	return {
		totalSize,
		allocated,
		free,
		usedPercent: totalSize > 0 ? (allocated / totalSize) * 100 : 0,
		health,
		online,
		errors,
		statusUnavailable,
		fragmentation: totalSize > 0 ? fragmentationWeighted / totalSize : 0,
		dedupRatio: dedupWeight > 0 ? dedupWeighted / dedupWeight : 1,
		dataVdevs,
		disks
	};
}

export function summarizeIO(values: ZFSDashboardPoolSnapshot[]): IOSummary {
	let readIOPS = 0;
	let writeIOPS = 0;
	let readBytesPerSecond = 0;
	let writeBytesPerSecond = 0;
	let latencyWeighted = 0;
	let latencyWeight = 0;
	let intervalSeconds = 10;
	let valid = false;

	for (const pool of values) {
		if (!pool.io.valid) continue;
		valid = true;
		readIOPS += pool.io.readIOPS;
		writeIOPS += pool.io.writeIOPS;
		readBytesPerSecond += pool.io.readBytesPerSecond;
		writeBytesPerSecond += pool.io.writeBytesPerSecond;
		intervalSeconds = Math.max(intervalSeconds, pool.io.intervalSeconds);
		if (pool.io.latencyAvailable && pool.io.readLatencyNanos !== null) {
			latencyWeighted += pool.io.readLatencyNanos * pool.io.readIOPS;
			latencyWeight += pool.io.readIOPS;
		}
		if (pool.io.latencyAvailable && pool.io.writeLatencyNanos !== null) {
			latencyWeighted += pool.io.writeLatencyNanos * pool.io.writeIOPS;
			latencyWeight += pool.io.writeIOPS;
		}
	}

	return {
		valid,
		readIOPS,
		writeIOPS,
		readBytesPerSecond,
		writeBytesPerSecond,
		averageLatency: latencyWeight > 0 ? latencyWeighted / latencyWeight / 1_000_000 : null,
		intervalSeconds
	};
}

export function aggregatePoolHistory(series: ZFSDashboardPoolSeries[]): PoolStatPoint[] {
	const buckets = new Map<number, PoolStatPoint[]>();
	for (const poolSeries of series) {
		for (const point of poolSeries.points) {
			const points = buckets.get(point.time) ?? [];
			points.push(point);
			buckets.set(point.time, points);
		}
	}

	return [...buckets.entries()]
		.map(([time, points]) => {
			const allocated = points.reduce((total, point) => total + point.allocated, 0);
			const size = points.reduce((total, point) => total + point.size, 0);
			const readIOPS = points.reduce((total, point) => total + point.readIOPS, 0);
			const writeIOPS = points.reduce((total, point) => total + point.writeIOPS, 0);
			const readLatencyWeight = points.reduce((total, point) => total + point.readIOPS, 0);
			const writeLatencyWeight = points.reduce((total, point) => total + point.writeIOPS, 0);
			return {
				id: Math.max(...points.map((point) => point.id)),
				time,
				health: points.reduce((health, point) => worseHealth(health, point.health), 'ONLINE'),
				worstHealth: points.reduce((health, point) => worseHealth(health, point.worstHealth), 'ONLINE'),
				allocated,
				free: points.reduce((total, point) => total + point.free, 0),
				size,
				fragmentation:
					size > 0
						? points.reduce((total, point) => total + point.fragmentation * point.size, 0) / size
						: 0,
				dedupRatio:
					allocated > 0
						? points.reduce((total, point) => total + point.dedupRatio * point.allocated, 0) / allocated
						: 1,
				readIOPS,
				writeIOPS,
				readBytesPerSecond: points.reduce((total, point) => total + point.readBytesPerSecond, 0),
				writeBytesPerSecond: points.reduce((total, point) => total + point.writeBytesPerSecond, 0),
				readLatencyNanos:
					readLatencyWeight > 0
						? points.reduce((total, point) => total + point.readLatencyNanos * point.readIOPS, 0) /
							readLatencyWeight
						: 0,
				writeLatencyNanos:
					writeLatencyWeight > 0
						? points.reduce((total, point) => total + point.writeLatencyNanos * point.writeIOPS, 0) /
							writeLatencyWeight
						: 0,
				maxReadIOPS: points.reduce((total, point) => total + point.maxReadIOPS, 0),
				maxWriteIOPS: points.reduce((total, point) => total + point.maxWriteIOPS, 0),
				maxReadBytesPerSecond: points.reduce((total, point) => total + point.maxReadBytesPerSecond, 0),
				maxWriteBytesPerSecond: points.reduce((total, point) => total + point.maxWriteBytesPerSecond, 0),
				maxReadLatencyNanos: Math.max(...points.map((point) => point.maxReadLatencyNanos)),
				maxWriteLatencyNanos: Math.max(...points.map((point) => point.maxWriteLatencyNanos)),
				sampleCount: points.reduce((total, point) => total + point.sampleCount, 0),
				intervalSeconds: Math.max(...points.map((point) => point.intervalSeconds))
			};
		})
		.sort((left, right) => left.time - right.time);
}

function parseScanTime(value: string): number {
	if (!value) return 0;
	if (/^\d+$/.test(value)) return Number(value) * 1000;
	const parsed = Date.parse(value);
	return Number.isNaN(parsed) ? 0 : parsed;
}

function scanName(value: string): string {
	return value.toLowerCase() === 'resilver' ? 'resilver' : 'scrub';
}

function completedDate(pool: ZFSDashboardPoolSnapshot): number {
	if (!pool.scan) return 0;
	return parseScanTime(pool.scan.endTime || pool.scan.startTime);
}

function formattedDate(timestamp: number): string {
	if (timestamp <= 0) return 'Completion time unavailable';
	return new Date(timestamp).toLocaleDateString([], {
		month: 'short',
		day: 'numeric',
		year: 'numeric'
	});
}

function relativeAge(timestamp: number): string {
	if (timestamp <= 0) return 'Completion time unavailable';
	const days = Math.max(0, Math.floor((Date.now() - timestamp) / 86_400_000));
	if (days === 0) return 'Completed today';
	if (days === 1) return 'Completed yesterday';
	return `Completed ${days} days ago`;
}

export function summarizeVerification(pools: ZFSDashboardPoolSnapshot[]): VerificationSummary {
	if (pools.length === 0) {
		return {
			value: 'No pools',
			detail: 'No pool verification data',
			footer: 'Import or create a pool to begin',
			tone: 'neutral',
			progress: null
		};
	}

	const active = pools.filter((pool) => {
		const state = pool.scan?.state.toUpperCase();
		return state === 'SCANNING' || state === 'PAUSED';
	});
	if (active.length > 0) {
		const pool = active[0];
		const scan = pool.scan!;
		const name = scanName(scan.function);
		const paused = scan.state.toUpperCase() === 'PAUSED';
		const progress = scan.progressPercent;
		const amount = name === 'resilver' && scan.processed > 0 ? scan.processed : scan.examined;
		const action = name === 'resilver' ? 'processed' : 'examined';
		return {
			value:
				active.length > 1
					? `${active.length} scans active`
					: `${name === 'resilver' ? 'Resilvering' : paused ? 'Scrub paused' : 'Scrubbing'}${progress === null ? '' : ` · ${progress.toFixed(0)}%`}`,
			detail:
				scan.toExamine > 0
					? `${formatBytesBinary(amount)} of ${formatBytesBinary(scan.toExamine)} ${action}`
					: `${pool.name} verification in progress`,
			footer: scan.errors > 0 ? `${scan.errors} scan errors reported` : 'No scan errors reported',
			tone: scan.errors > 0 ? 'danger' : paused ? 'warning' : 'neutral',
			progress
		};
	}

	const scanErrors = pools.filter((pool) => (pool.scan?.errors ?? 0) > 0);
	if (scanErrors.length > 0) {
		const pool = scanErrors[0];
		const errors = pool.scan?.errors ?? 0;
		return {
			value: pools.length > 1 ? `${scanErrors.length} pool${scanErrors.length === 1 ? '' : 's'} need attention` : 'Errors found',
			detail: `${pool.name} ${scanName(pool.scan?.function ?? '')} reported ${errors} error${errors === 1 ? '' : 's'}`,
			footer: relativeAge(completedDate(pool)),
			tone: 'danger',
			progress: null
		};
	}

	const incomplete = pools.filter((pool) => {
		const state = pool.scan?.state.toUpperCase();
		return state === 'CANCELED' || state === 'CANCELLED' || state === 'INTERRUPTED';
	});
	if (incomplete.length > 0) {
		const pool = incomplete[0];
		return {
			value: pools.length > 1 ? `${incomplete.length} verification incomplete` : 'Verification incomplete',
			detail: `${pool.name} ${scanName(pool.scan?.function ?? '')} was not completed`,
			footer: relativeAge(completedDate(pool)),
			tone: 'warning',
			progress: null
		};
	}

	const unknown = pools.filter((pool) => !pool.statusAvailable);
	if (unknown.length > 0) {
		return {
			value: 'Verification unknown',
			detail: `${unknown.length} pool${unknown.length === 1 ? '' : 's'} could not be inspected`,
			footer: 'Detailed pool status is unavailable',
			tone: 'warning',
			progress: null
		};
	}

	const unverified = pools.filter((pool) => pool.scan === null);
	if (unverified.length > 0) {
		return {
			value: pools.length > 1 ? `${unverified.length} pool${unverified.length === 1 ? '' : 's'} unverified` : 'Never scrubbed',
			detail: pools.length > 1 ? `No scrub recorded for ${unverified[0].name}` : 'No completed scrub is recorded',
			footer: 'Run a scrub to verify stored data',
			tone: 'warning',
			progress: null
		};
	}

	const oldest = [...pools].sort((left, right) => completedDate(left) - completedDate(right))[0];
	const timestamp = completedDate(oldest);
	const name = scanName(oldest.scan?.function ?? '');
	return {
		value:
			pools.length > 1
				? 'All scans clean'
				: name === 'resilver'
					? 'Resilver complete'
					: 'Last scrub clean',
		detail:
			pools.length > 1
				? `Oldest verification ${formattedDate(timestamp)}`
				: `${formattedDate(timestamp)} · 0 errors`,
		footer: relativeAge(timestamp),
		tone: 'success',
		progress: null
	};
}

export function compactNumber(value: number): string {
	return new Intl.NumberFormat(undefined, {
		notation: 'compact',
		maximumFractionDigits: 1
	}).format(value);
}

function resolutionLabel(seconds: number): string {
	if (seconds < 60) return `${seconds}-second`;
	if (seconds < 3600) return `${Math.round(seconds / 60)}-minute`;
	return `${Math.round(seconds / 3600)}-hour`;
}

export function historyWindowLabel(points: PoolStatPoint[], resolutionSeconds: number): string {
	if (points.length === 0) return 'Waiting for historical samples';
	const first = new Date(points[0].time).toLocaleString([], {
		month: 'short',
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit'
	});
	const last = new Date(points[points.length - 1].time).toLocaleString([], {
		month: 'short',
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit'
	});
	return `${first} – ${last} · up to ${resolutionLabel(resolutionSeconds)} averages`;
}

export function statusTone(health: string, errors: number, unavailable: number): Tone {
	if (healthRank(health) >= 4 || errors > 0) return 'danger';
	if (health.toUpperCase() !== 'ONLINE' || unavailable > 0) return 'warning';
	return 'success';
}

export function capacityTone(usedPercent: number): Tone {
	if (usedPercent >= 90) return 'danger';
	if (usedPercent >= 80) return 'warning';
	return 'neutral';
}

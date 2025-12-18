import { GZFSDatasetTypeSchema, type Dataset, type GroupedByPool } from '$lib/types/zfs/dataset';
import type { Zpool } from '$lib/types/zfs/pool';

export function groupByPool(
	pools: Zpool[] | undefined,
	datasets: Dataset[] | undefined
): GroupedByPool[] {
	if (!pools || !datasets) return [];

	return pools.map((pool) => ({
		name: pool.name,
		pool,
		filesystems: datasets.filter(
			(d) => d.name.startsWith(pool.name) && d.type === GZFSDatasetTypeSchema.enum.FILESYSTEM
		),
		snapshots: datasets.filter(
			(d) => d.name.startsWith(pool.name) && d.type === GZFSDatasetTypeSchema.enum.SNAPSHOT
		),
		volumes: datasets.filter(
			(d) => d.name.startsWith(pool.name) && d.type === GZFSDatasetTypeSchema.enum.VOLUME
		)
	}));
}

export function groupByPoolNames(
	poolNames: string[] | undefined,
	datasets: Dataset[] | undefined
): GroupedByPool[] {
	if (!poolNames || !datasets) return [];

	return poolNames.map((name) => ({
		name,
		pool: name,
		filesystems: datasets.filter(
			(d) => d.name.startsWith(name) && d.type === GZFSDatasetTypeSchema.enum.FILESYSTEM
		),
		snapshots: datasets.filter(
			(d) => d.name.startsWith(name) && d.type === GZFSDatasetTypeSchema.enum.SNAPSHOT
		),
		volumes: datasets.filter(
			(d) => d.name.startsWith(name) && d.type === GZFSDatasetTypeSchema.enum.VOLUME
		)
	}));
}

export function getDatasetByGUID(
	datasets: Dataset[] | undefined,
	guid: string
): Dataset | undefined {
	if (!datasets) {
		return undefined;
	}

	const dataset = datasets.find((dataset) => dataset.guid === guid);
	return dataset;
}

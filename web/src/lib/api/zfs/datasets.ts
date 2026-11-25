import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	DatasetSchema,
	DetailedDatasetSchema,
	GZFSDatasetTypeSchema,
	PeriodicSnapshotSchema,
	SnapshotSummarySchema,
	type Dataset,
	type DetailedDataset,
	type GZFSDatasetType,
	type PeriodicSnapshot,
	type SnapshotSummary
} from '$lib/types/zfs/dataset';

import { apiRequestData, apiRequestResult, type NodeAPIDataRequestOptions } from '$lib/utils/http';

export async function getDatasets(
	type: GZFSDatasetType = GZFSDatasetTypeSchema.enum.ALL,
	options?: NodeAPIDataRequestOptions
): Promise<Dataset[]> {
	return await apiRequestData(
		`/zfs/datasets?type=${type}`,
		DatasetSchema.array(),
		'GET',
		undefined,
		options
	);
}

export async function getDatasetsResult(
	type: GZFSDatasetType,
	hostname: string,
	signal?: AbortSignal
): Promise<Dataset[] | APIResponse> {
	return await apiRequestResult(
		`/zfs/datasets?type=${type}`,
		DatasetSchema.array(),
		'GET',
		undefined,
		{
			hostname,
			signal
		}
	);
}

export async function getDetailedDatasetsResult(
	type: GZFSDatasetType,
	hostname: string,
	signal?: AbortSignal
): Promise<DetailedDataset[] | APIResponse> {
	return await apiRequestResult(
		`/zfs/datasets?type=${type}`,
		DetailedDatasetSchema.array(),
		'GET',
		undefined,
		{ hostname, signal }
	);
}

export async function rollbackSnapshot(
	snapshot: Pick<SnapshotSummary, 'guid' | 'name'>,
	expectedNewer: string[],
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/zfs/datasets/snapshot/${encodeURIComponent(snapshot.guid)}/rollback`,
		APIResponseSchema,
		'POST',
		{ name: snapshot.name, destroyMoreRecent: expectedNewer.length > 0, expectedNewer },
		options
	);
}

export async function createSnapshot(
	dataset: Pick<Dataset, 'guid'>,
	name: string,
	recursive: boolean,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		'/zfs/datasets/snapshot',
		APIResponseSchema,
		'POST',
		{ name, recursive, guid: dataset.guid },
		options
	);
}

export async function getSnapshotSummariesResult(
	hostname: string,
	signal?: AbortSignal
): Promise<SnapshotSummary[] | APIResponse> {
	return await apiRequestResult('/zfs/snapshots', SnapshotSummarySchema.array(), 'GET', undefined, {
		hostname,
		signal
	});
}

export async function getPeriodicSnapshotsResult(
	hostname: string,
	signal?: AbortSignal
): Promise<PeriodicSnapshot[] | APIResponse> {
	return await apiRequestResult(
		'/zfs/datasets/snapshot/periodic',
		PeriodicSnapshotSchema.array(),
		'GET',
		undefined,
		{ hostname, signal }
	);
}

export async function getPeriodicSnapshots(): Promise<PeriodicSnapshot[]> {
	return await apiRequestData(
		'/zfs/datasets/snapshot/periodic',
		PeriodicSnapshotSchema.array(),
		'GET'
	);
}

export async function createPeriodicSnapshot(
	dataset: Dataset,
	prefix: string,
	recursive: boolean,
	interval: number,
	cronExpr: string,
	retentionType: 'none' | 'simple' | 'gfs',
	keepLast: number | null = null,
	maxAgeDays: number | null = null,
	keepHourly: number | null = null,
	keepDaily: number | null = null,
	keepWeekly: number | null = null,
	keepMonthly: number | null = null,
	keepYearly: number | null = null
): Promise<APIResponse> {
	return await apiRequestResult('/zfs/datasets/snapshot/periodic', APIResponseSchema, 'POST', {
		guid: dataset.guid,
		prefix: prefix,
		recursive: recursive,
		interval: interval,
		cronExpr: cronExpr,
		retentionType: retentionType,
		keepLast: keepLast,
		maxAgeDays: maxAgeDays,
		keepHourly: keepHourly,
		keepDaily: keepDaily,
		keepWeekly: keepWeekly,
		keepMonthly: keepMonthly,
		keepYearly: keepYearly
	});
}

export async function modifyPeriodicSnapshot(
	id: number,
	interval: number,
	cronExpr: string,
	retentionType: 'none' | 'simple' | 'gfs',
	keepLast: number | null,
	maxAgeDays: number | null,
	keepHourly: number | null,
	keepDaily: number | null = null,
	keepWeekly: number | null = null,
	keepMonthly: number | null = null,
	keepYearly: number | null = null
): Promise<APIResponse> {
	return await apiRequestResult(
		`/zfs/datasets/snapshot/periodic/${id}`,
		APIResponseSchema,
		'PATCH',
		{
			interval: interval,
			cronExpr: cronExpr,
			retentionType: retentionType,
			keepLast: keepLast,
			maxAgeDays: maxAgeDays,
			keepHourly: keepHourly,
			keepDaily: keepDaily,
			keepWeekly: keepWeekly,
			keepMonthly: keepMonthly,
			keepYearly: keepYearly
		}
	);
}

export interface SnapshotScheduleSettings {
	interval: number;
	cronExpr: string;
	retentionType: 'none' | 'simple' | 'gfs';
	keepLast: number;
	maxAgeDays: number;
	keepHourly: number;
	keepDaily: number;
	keepWeekly: number;
	keepMonthly: number;
	keepYearly: number;
}

export async function createSnapshotSchedule(
	dataset: Pick<Dataset, 'guid'>,
	prefix: string,
	recursive: boolean,
	settings: SnapshotScheduleSettings,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		'/zfs/datasets/snapshot/periodic',
		APIResponseSchema,
		'POST',
		{ guid: dataset.guid, prefix, recursive, ...settings },
		options
	);
}

export async function editSnapshotSchedule(
	id: number,
	settings: SnapshotScheduleSettings,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/zfs/datasets/snapshot/periodic/${id}`,
		APIResponseSchema,
		'PATCH',
		settings,
		options
	);
}

export async function bulkDeletePeriodicSnapshots(
	ids: number[],
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		'/zfs/datasets/snapshot/periodic',
		APIResponseSchema,
		'DELETE',
		{ ids },
		options
	);
}

export async function createFileSystem(
	name: string,
	parent: string,
	properties: Record<string, string | undefined>,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		'/zfs/datasets/filesystem',
		APIResponseSchema,
		'POST',
		{ name, parent, properties },
		options
	);
}

export async function editFileSystem(
	guid: string,
	properties: Record<string, string | undefined>,
	options?: NodeAPIDataRequestOptions,
	inherit: string[] = []
): Promise<APIResponse> {
	return await apiRequestResult(
		`/zfs/datasets/filesystem/${guid}`,
		APIResponseSchema,
		'PATCH',
		{ properties, inherit },
		options
	);
}

export async function deleteFileSystem(
	dataset: Dataset,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/zfs/datasets/filesystem/${dataset.guid}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		options
	);
}

export async function createVolume(
	name: string,
	parent: string,
	props: Record<string, string>,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		'/zfs/datasets/volume',
		APIResponseSchema,
		'POST',
		{ name, parent, properties: props },
		options
	);
}

export async function editVolume(
	guid: string,
	properties: Record<string, string>,
	options?: NodeAPIDataRequestOptions,
	inherit: string[] = []
): Promise<APIResponse> {
	return await apiRequestResult(
		`/zfs/datasets/volume/${guid}`,
		APIResponseSchema,
		'PATCH',
		{ properties, inherit },
		options
	);
}

export async function deleteVolume(dataset: Dataset): Promise<APIResponse> {
	return await apiRequestResult(
		`/zfs/datasets/volume/${dataset.guid}`,
		APIResponseSchema,
		'DELETE'
	);
}

export async function bulkDelete(
	datasets: Pick<Dataset, 'name' | 'guid'>[],
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	const query = new URLSearchParams();
	for (const dataset of datasets) {
		query.append('name', dataset.name);
		query.append('guid', dataset.guid);
	}
	return await apiRequestResult(
		`/zfs/datasets?${query.toString()}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		options
	);
}

export async function flashVolume(
	guid: string,
	uuid: string,
	options?: NodeAPIDataRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/zfs/datasets/volume/${guid}/flash`,
		APIResponseSchema,
		'POST',
		{ uuid },
		options
	);
}

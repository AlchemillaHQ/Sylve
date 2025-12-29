import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	DatasetSchema,
	GZFSDatasetTypeSchema,
	PaginatedDatasetsResponseSchema,
	PeriodicSnapshotSchema,
	type Dataset,
	type GZFSDatasetType,
	type PaginatedDatasetsResponse,
	type PeriodicSnapshot
} from '$lib/types/zfs/dataset';

import { apiRequest } from '$lib/utils/http';

export async function getDatasets(
	type: GZFSDatasetType = GZFSDatasetTypeSchema.enum.ALL
): Promise<Dataset[]> {
	return await apiRequest(`/zfs/datasets?type=${type}`, DatasetSchema.array(), 'GET');
}

export async function deleteSnapshot(
	snapshot: Dataset,
	recursive: boolean = false
): Promise<APIResponse> {
	const param = recursive ? '?recursive=true' : '';
	return await apiRequest(
		`/zfs/datasets/snapshot/${snapshot.guid}${param}`,
		APIResponseSchema,
		'DELETE'
	);
}

export async function createSnapshot(
	dataset: Dataset,
	name: string,
	recursive: boolean
): Promise<APIResponse> {
	return await apiRequest('/zfs/datasets/snapshot', APIResponseSchema, 'POST', {
		name: name,
		recursive: recursive,
		guid: dataset.guid
	});
}

export async function getPeriodicSnapshots(): Promise<PeriodicSnapshot[]> {
	return await apiRequest('/zfs/datasets/snapshot/periodic', PeriodicSnapshotSchema.array(), 'GET');
}

export async function createPeriodicSnapshot(
	dataset: Dataset,
	prefix: string,
	recursive: boolean,
	interval: number,
	cronExpr: string,
	keepLast: number | null = null,
	maxAgeDays: number | null = null,
	keepHourly: number | null = null,
	keepDaily: number | null = null,
	keepWeekly: number | null = null,
	keepMonthly: number | null = null,
	keepYearly: number | null = null
): Promise<APIResponse> {
	return await apiRequest('/zfs/datasets/snapshot/periodic', APIResponseSchema, 'POST', {
		guid: dataset.guid,
		prefix: prefix,
		recursive: recursive,
		interval: interval,
		cronExpr: cronExpr,
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
	keepLast: number | null,
	maxAgeDays: number | null,
	keepHourly: number | null,
	keepDaily: number | null = null,
	keepWeekly: number | null = null,
	keepMonthly: number | null = null,
	keepYearly: number | null = null
): Promise<APIResponse> {
	return await apiRequest(`/zfs/datasets/snapshot/periodic`, APIResponseSchema, 'PATCH', {
		id: id,
		keepLast: Number(keepLast) || null,
		maxAgeDays: Number(maxAgeDays) || null,
		keepHourly: Number(keepHourly) || null,
		keepDaily: Number(keepDaily) || null,
		keepWeekly: Number(keepWeekly) || null,
		keepMonthly: Number(keepMonthly) || null,
		keepYearly: Number(keepYearly) || null
	});
}

export async function deletePeriodicSnapshot(guid: string): Promise<APIResponse> {
	return await apiRequest(`/zfs/datasets/snapshot/periodic/${guid}`, APIResponseSchema, 'DELETE');
}

export async function createFileSystem(
	name: string,
	parent: string,
	properties: Record<string, string | undefined>
): Promise<APIResponse> {
	return await apiRequest('/zfs/datasets/filesystem', APIResponseSchema, 'POST', {
		name: name,
		parent: parent,
		properties: properties
	});
}

export async function editFileSystem(
	guid: string,
	properties: Record<string, string | undefined>
): Promise<APIResponse> {
	return await apiRequest(`/zfs/datasets/filesystem`, APIResponseSchema, 'PATCH', {
		guid: guid,
		properties: properties
	});
}

export async function deleteFileSystem(dataset: Dataset): Promise<APIResponse> {
	return await apiRequest(`/zfs/datasets/filesystem/${dataset.guid}`, APIResponseSchema, 'DELETE');
}

export async function rollbackSnapshot(guid: string): Promise<APIResponse> {
	return await apiRequest(`/zfs/datasets/snapshot/rollback`, APIResponseSchema, 'POST', {
		guid: guid,
		destroyMoreRecent: true
	});
}

export async function createVolume(
	name: string,
	parent: string,
	props: Record<string, string>
): Promise<APIResponse> {
	return await apiRequest('/zfs/datasets/volume', APIResponseSchema, 'POST', {
		name: name,
		parent: parent,
		properties: props
	});
}

export async function editVolume(
	guid: string,
	properties: Record<string, string>
): Promise<APIResponse> {
	return await apiRequest('/zfs/datasets/volume', APIResponseSchema, 'PATCH', {
		guid,
		properties: properties
	});
}

export async function deleteVolume(dataset: Dataset): Promise<APIResponse> {
	return await apiRequest(`/zfs/datasets/volume/${dataset.guid}`, APIResponseSchema, 'DELETE');
}

export async function bulkDelete(datasets: Dataset[]): Promise<APIResponse> {
	const guids = datasets.map((dataset) => dataset.guid);
	return await apiRequest('/zfs/datasets/bulk-delete', APIResponseSchema, 'POST', {
		guids: guids
	});
}

export async function bulkDeleteByNames(datasets: Dataset[]): Promise<APIResponse> {
	const names = datasets.map((dataset) => dataset.name);
	return await apiRequest('/zfs/datasets/bulk-delete-by-names', APIResponseSchema, 'POST', {
		names: names
	});
}

export async function flashVolume(guid: string, uuid: string): Promise<APIResponse> {
	return await apiRequest('/zfs/datasets/volume/flash', APIResponseSchema, 'POST', {
		guid: guid,
		uuid: uuid
	});
}

export async function getPaginatedDatasets(
	page: number,
	pageSize: number,
	datasetType: GZFSDatasetType,
	search: string = ''
): Promise<PaginatedDatasetsResponse> {
	return await apiRequest(
		`/zfs/datasets/paginated?datasetType=${datasetType}&limit=${pageSize}&offset=${(page - 1) * pageSize}&search=${encodeURIComponent(search)}`,
		PaginatedDatasetsResponseSchema,
		'GET'
	);
}

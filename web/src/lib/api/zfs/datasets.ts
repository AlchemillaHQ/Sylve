import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { DatasetSchema, type Dataset } from '$lib/types/zfs/dataset';

import { apiRequest } from '$lib/utils/http';

export async function getDatasets(): Promise<Dataset[]> {
	return await apiRequest('/zfs/datasets', DatasetSchema.array(), 'GET');
}

export async function deleteSnapshot(snapshot: Dataset): Promise<APIResponse> {
	return await apiRequest(
		`/zfs/datasets/snapshot/${snapshot.properties.guid}`,
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
		guid: dataset.properties.guid
	});
}

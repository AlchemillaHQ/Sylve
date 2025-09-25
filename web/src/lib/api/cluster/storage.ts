import { ClusterStoragesSchema, type ClusterStorages } from '$lib/types/cluster/storage';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';

export async function getStorages(): Promise<ClusterStorages> {
	return await apiRequest('/cluster/storage', ClusterStoragesSchema, 'GET');
}

export async function createS3Storage(
	name: string,
	endpoint: string,
	region: string,
	bucket: string,
	accessKey: string,
	secretKey: string
): Promise<APIResponse> {
	return await apiRequest('/cluster/storage/s3', APIResponseSchema, 'POST', {
		name,
		endpoint,
		region,
		bucket,
		accessKey,
		secretKey
	});
}

export async function deleteS3Storage(id: number): Promise<APIResponse> {
	return await apiRequest(`/cluster/storage/s3/${id}`, APIResponseSchema, 'DELETE');
}

export async function createDirStorage(name: string, path: string): Promise<APIResponse> {
	return await apiRequest('/cluster/storage/directory', APIResponseSchema, 'POST', {
		name,
		path
	});
}

export async function deleteDirStorage(id: number): Promise<APIResponse> {
	return await apiRequest(`/cluster/storage/directory/${id}`, APIResponseSchema, 'DELETE');
}

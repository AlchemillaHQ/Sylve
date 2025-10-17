import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { DownloadSchema, type Download } from '$lib/types/utilities/downloader';
import { apiRequest } from '$lib/utils/http';

export async function getDownloads(): Promise<Download[]> {
	return await apiRequest('/utilities/downloads', DownloadSchema.array(), 'GET');
}

export async function startDownload(
	url: string,
	filename?: string,
	ignoreTLS?: boolean
): Promise<APIResponse> {
	return await apiRequest('/utilities/downloads', APIResponseSchema, 'POST', {
		url,
		filename,
		ignoreTLS
	});
}

export async function deleteDownload(id: number): Promise<APIResponse> {
	return await apiRequest(`/utilities/downloads/${id}`, APIResponseSchema, 'DELETE');
}

export async function bulkDeleteDownloads(ids: number[]): Promise<APIResponse> {
	return await apiRequest('/utilities/downloads/bulk-delete', APIResponseSchema, 'POST', {
		ids
	});
}

export async function getSignedURL(name: string, parentUUID: string): Promise<APIResponse> {
	return await apiRequest('/utilities/downloads/signed-url', APIResponseSchema, 'POST', {
		name,
		parentUUID
	});
}

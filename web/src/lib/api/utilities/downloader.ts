import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	DownloadSchema,
	UTypeGroupedDownloadSchema,
	type Download,
	type UTypeGroupedDownload
} from '$lib/types/utilities/downloader';
import { apiRequest } from '$lib/utils/http';

export async function getDownloads(): Promise<Download[]> {
	return await apiRequest('/utilities/downloads', DownloadSchema.array(), 'GET');
}

export async function getDownloadsByUType(): Promise<UTypeGroupedDownload[]> {
	return await apiRequest('/utilities/downloads/utype', UTypeGroupedDownloadSchema.array(), 'GET');
}

export async function startDownload(
	url: string,
	downloadType: 'base-rootfs' | 'uncategorized',
	filename?: string,
	ignoreTLS?: boolean,
	automaticExtraction?: boolean
): Promise<APIResponse> {
	return await apiRequest('/utilities/downloads', APIResponseSchema, 'POST', {
		url,
		filename,
		ignoreTLS,
		automaticExtraction,
		downloadType
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

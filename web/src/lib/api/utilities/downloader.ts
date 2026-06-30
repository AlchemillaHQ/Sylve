import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	DownloadPathsSchema,
	DownloadSchema,
	UTypeGroupedDownloadSchema,
	type DownloadPaths,
	type Download,
	type UTypeGroupedDownload
} from '$lib/types/utilities/downloader';
import { apiRequest } from '$lib/utils/http';

export async function getDownloads(hostname?: string): Promise<Download[]> {
	return await apiRequest('/utilities/downloads', DownloadSchema.array(), 'GET', undefined, { hostname });
}

export async function getDownloadsByUType(hostname?: string): Promise<UTypeGroupedDownload[]> {
	return await apiRequest('/utilities/downloads/utype', UTypeGroupedDownloadSchema.array(), 'GET', undefined, { hostname });
}

export async function getDownloadPaths(): Promise<DownloadPaths> {
	return await apiRequest('/utilities/downloads/paths', DownloadPathsSchema, 'GET');
}

export async function startDownload(
	url: string,
	downloadType: 'base-rootfs' | 'cloud-init' | 'uncategorized',
	filename?: string,
	ignoreTLS?: boolean,
	automaticExtraction?: boolean,
	automaticRawConversion?: boolean
): Promise<APIResponse> {
	return await apiRequest('/utilities/downloads', APIResponseSchema, 'POST', {
		url,
		filename,
		ignoreTLS,
		automaticExtraction,
		automaticRawConversion,
		downloadType
	});
}

export async function updateDownload(
	id: number,
	data: {
		name?: string;
		uType?: string;
		automaticExtraction?: boolean;
		automaticRawConversion?: boolean;
	}
): Promise<APIResponse> {
	return await apiRequest(`/utilities/downloads/${id}`, APIResponseSchema, 'PUT', data);
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

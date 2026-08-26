import type { APIResponse } from '$lib/types/common';
import {
	DownloadSchema,
	DownloadDeleteResultSchema,
	DownloadStartResultSchema,
	DownloaderUploadAbortSchema,
	DownloaderUploadCompletionSchema,
	SignedDownloadURLResultSchema,
	UTypeGroupedDownloadSchema,
	type Download,
	type DownloadDeleteResult,
	type DownloadStartResult,
	type DownloadType,
	type DownloaderUploadAbort,
	type DownloaderUploadCompletion,
	type SignedDownloadURLResult,
	type UTypeGroupedDownload
} from '$lib/types/utilities/downloader';
import { apiRequest } from '$lib/utils/http';
import type { NodeAPIRequestOptions } from '$lib/utils/http';

export async function getDownloadsResult(
	options?: NodeAPIRequestOptions
): Promise<Download[] | APIResponse> {
	return await apiRequest('/utilities/downloads', DownloadSchema.array(), 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export async function getDownloadsByUTypeResult(
	options?: NodeAPIRequestOptions
): Promise<UTypeGroupedDownload[] | APIResponse> {
	return await apiRequest(
		'/utilities/downloads/utype',
		UTypeGroupedDownloadSchema.array(),
		'GET',
		undefined,
		{ ...options, preserveErrors: true }
	);
}

export async function startDownload(
	url: string,
	downloadType: DownloadType,
	filename?: string,
	ignoreTLS?: boolean,
	automaticExtraction?: boolean,
	automaticRawConversion?: boolean,
	hostname?: string
): Promise<DownloadStartResult | APIResponse> {
	return await apiRequest(
		'/utilities/downloads',
		DownloadStartResultSchema,
		'POST',
		{
			url,
			filename,
			ignoreTLS,
			automaticExtraction,
			automaticRawConversion,
			downloadType
		},
		{ preserveErrors: true, hostname }
	);
}

export async function completeDownloaderUpload(
	uploadId: string,
	downloadType: DownloadType,
	automaticExtraction: boolean,
	automaticRawConversion: boolean,
	hostname?: string
): Promise<DownloaderUploadCompletion | APIResponse> {
	return await apiRequest(
		`/utilities/downloader-uploads/${encodeURIComponent(uploadId)}/complete`,
		DownloaderUploadCompletionSchema,
		'POST',
		{
			downloadType,
			automaticExtraction,
			automaticRawConversion
		},
		{ preserveErrors: true, hostname }
	);
}

export async function abortDownloaderUpload(
	uploadId: string,
	hostname?: string
): Promise<DownloaderUploadAbort | APIResponse> {
	return await apiRequest(
		`/utilities/downloader-uploads/${encodeURIComponent(uploadId)}`,
		DownloaderUploadAbortSchema,
		'DELETE',
		undefined,
		{ preserveErrors: true, hostname }
	);
}

export async function updateDownload(
	id: number,
	data: {
		name?: string;
		uType?: DownloadType;
		automaticExtraction?: boolean;
		automaticRawConversion?: boolean;
	},
	hostname?: string
): Promise<Download | APIResponse> {
	return await apiRequest(`/utilities/downloads/${id}`, DownloadSchema, 'PATCH', data, {
		preserveErrors: true,
		hostname
	});
}

export async function deleteDownload(
	id: number,
	hostname?: string
): Promise<DownloadDeleteResult | APIResponse> {
	return await apiRequest(
		`/utilities/downloads/${id}`,
		DownloadDeleteResultSchema,
		'DELETE',
		undefined,
		{ preserveErrors: true, hostname }
	);
}

export async function bulkDeleteDownloads(
	ids: number[],
	hostname?: string
): Promise<DownloadDeleteResult | APIResponse> {
	return await apiRequest(
		'/utilities/downloads/bulk-delete',
		DownloadDeleteResultSchema,
		'POST',
		{ ids },
		{ preserveErrors: true, hostname }
	);
}

export async function getSignedURL(
	name: string,
	parentUUID: string,
	hostname?: string
): Promise<SignedDownloadURLResult | APIResponse> {
	return await apiRequest(
		'/utilities/downloads/signed-url',
		SignedDownloadURLResultSchema,
		'POST',
		{
			name,
			parentUUID
		},
		{ preserveErrors: true, hostname }
	);
}

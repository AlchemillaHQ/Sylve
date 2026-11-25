import { nodePath } from '$lib/api/common';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	DownloadTicketSchema,
	FileListingSchema,
	FileNodeSchema,
	type DownloadTicket,
	type FileListing,
	type FileNode
} from '$lib/types/system/file-explorer';
import { apiRequest, isAPIResponse } from '$lib/utils/http';

export async function getFolderListing(
	path: string,
	options: { limit?: number; query?: string },
	hostname?: string,
	signal?: AbortSignal
): Promise<FileListing | APIResponse> {
	const params = new URLSearchParams({ id: path });
	if (options.limit) params.set('limit', String(options.limit));
	if (options.query) params.set('q', options.query);
	return await apiRequest(
		`/system/file-explorer/list?${params}`,
		FileListingSchema,
		'GET',
		undefined,
		{ hostname, signal, preserveErrors: true }
	);
}

export async function createDownloadTicket(
	kind: 'file' | 'archive',
	ids: string[]
): Promise<DownloadTicket | APIResponse> {
	return await apiRequest(
		'/auth/download-tickets',
		DownloadTicketSchema,
		'POST',
		{ kind, ids },
		{
			preserveErrors: true,
			skipAuditLog: true
		}
	);
}

export function fileExplorerDownloadQuery(ids: string[], ticket = ''): URLSearchParams {
	const query = new URLSearchParams(ids.map((id) => ['id', id]));
	if (ticket) query.set('ticket', ticket);
	return query;
}

export async function fileExplorerDownloadURL(
	kind: 'file' | 'archive',
	ids: string | string[],
	hostname?: string
): Promise<string | APIResponse> {
	const list = typeof ids === 'string' ? [ids] : ids;
	const ticket = await createDownloadTicket(kind, list);
	if (isAPIResponse(ticket)) return ticket;
	const route = kind === 'file' ? 'download' : 'archive';
	const query = fileExplorerDownloadQuery(list, ticket.ticket);
	return `${nodePath(`/api/system/file-explorer/${route}`, hostname)}?${query}`;
}

export async function getFiles(
	id?: string,
	hostname?: string,
	signal?: AbortSignal
): Promise<FileNode[] | APIResponse> {
	let url = '/system/file-explorer';

	if (id) {
		url += `?id=${encodeURIComponent(id)}`;
	}

	return await apiRequest(url, FileNodeSchema.array(), 'GET', undefined, {
		hostname,
		signal,
		preserveErrors: true
	});
}

export async function addFileOrFolder(
	path: string,
	name: string,
	isFolder: boolean,
	hostname?: string,
	size?: number
): Promise<APIResponse> {
	const body = {
		path,
		name,
		isFolder,
		size
	};

	return await apiRequest('/system/file-explorer', APIResponseSchema, 'POST', body, { hostname });
}

export async function revertFileExplorerUpload(
	uploadId: string,
	hostname?: string
): Promise<APIResponse> {
	return await apiRequest(
		'/system/file-explorer/upload',
		APIResponseSchema,
		'DELETE',
		{ data: { uploadId } },
		{ hostname, preserveErrors: true }
	);
}

export async function renameFileOrFolder(
	id: string,
	newName: string,
	hostname?: string
): Promise<APIResponse> {
	const body = {
		id,
		newName
	};

	return await apiRequest('/system/file-explorer/rename', APIResponseSchema, 'POST', body, {
		hostname
	});
}

export async function deleteFilesOrFolders(
	paths: string[],
	hostname?: string,
	signal?: AbortSignal
): Promise<APIResponse> {
	const body = {
		paths
	};

	return await apiRequest('/system/file-explorer/delete', APIResponseSchema, 'POST', body, {
		hostname,
		signal
	});
}

export async function copyOrMoveFilesOrFolders(
	items: { source: string; destination: string }[],
	move: boolean,
	hostname?: string,
	signal?: AbortSignal
): Promise<APIResponse> {
	const body = {
		items,
		move
	};

	return await apiRequest(
		'/system/file-explorer/copy-or-move-batch',
		APIResponseSchema,
		'POST',
		body,
		{ hostname, signal }
	);
}

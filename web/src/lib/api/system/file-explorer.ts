import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { FileNodeSchema, type FileNode } from '$lib/types/system/file-explorer';
import { apiRequest } from '$lib/utils/http';

export async function getFiles(id?: string, hostname?: string): Promise<FileNode[] | APIResponse> {
	let url = '/system/file-explorer';

	if (id) {
		url += `?id=${encodeURIComponent(id)}`;
	}

	return await apiRequest(url, FileNodeSchema.array(), 'GET', undefined, {
		hostname,
		preserveErrors: true
	});
}

export async function addFileOrFolder(
	path: string,
	name: string,
	isFolder: boolean,
	hostname?: string
): Promise<APIResponse> {
	const body = {
		path,
		name,
		isFolder
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
	hostname?: string
): Promise<APIResponse> {
	const body = {
		paths
	};

	return await apiRequest('/system/file-explorer/delete', APIResponseSchema, 'POST', body, {
		hostname
	});
}

export async function copyOrMoveFilesOrFolders(
	items: { source: string; destination: string }[],
	move: boolean,
	hostname?: string
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
		{ hostname }
	);
}

import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { FileNodeSchema, type FileNode } from '$lib/types/system/file-explorer';
import { apiRequest } from '$lib/utils/http';

export async function getFiles(id?: string): Promise<FileNode[]> {
	let url = '/system/file-explorer';

	if (id) {
		url += `?id=${encodeURIComponent(id)}`;
	}

	return await apiRequest(url, FileNodeSchema.array(), 'GET');
}

export async function addFileOrFolder(
	path: string,
	name: string,
	isFolder: boolean
): Promise<APIResponse> {
	const body = {
		path,
		name,
		isFolder
	};

	return await apiRequest('/system/file-explorer', APIResponseSchema, 'POST', body);
}

export async function deleteFileOrFolder(path: string): Promise<APIResponse> {
	return await apiRequest(
		'/system/file-explorer?id=' + encodeURIComponent(path),
		APIResponseSchema,
		'DELETE'
	);
}

export async function renameFileOrFolder(id: string, newName: string): Promise<APIResponse> {
	const body = {
		id,
		newName
	};

	return await apiRequest('/system/file-explorer/rename', APIResponseSchema, 'POST', body);
}

export async function copyOrMoveFileOrFolder(
	id: string,
	newPath: string,
	cut: boolean
): Promise<APIResponse> {
	const body = {
		id,
		newPath,
		cut
	};

	return await apiRequest('/system/file-explorer/copy-or-move', APIResponseSchema, 'POST', body);
}

export async function deleteFilesOrFolders(paths: string[]): Promise<APIResponse> {
	const body = {
		paths
	};

	return await apiRequest('/system/file-explorer/delete', APIResponseSchema, 'POST', body);
}

export async function copyOrMoveFilesOrFolders(
	pairs: [string, string][],
	cut: boolean
): Promise<APIResponse> {
	const body = {
		pairs,
		cut
	};

	return await apiRequest(
		'/system/file-explorer/copy-or-move-batch',
		APIResponseSchema,
		'POST',
		body
	);
}

/*
func (s *Service) DoesPathHaveBase(root string) (bool, error) {
	if root == "" {
		return false, fmt.Errorf("path_required")
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return false, fmt.Errorf("path_does_not_exist: %s", root)
		}
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("not_a_directory: %s", root)
	}

	required := []string{
		"bin/freebsd-version",
		"bin/sh",
		"libexec/ld-elf.so.1",
		"lib/libc.so.7",
		"etc/os-release",
	}

	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			return false, nil
		}
	}

	return true, nil
}
*/

export async function doesPathHaveBase(path: string): Promise<boolean> {
	const entries = await getFiles(path);
	const required = [`${path}/bin`, `${path}/libexec`, `${path}/lib`, `${path}/etc`];

	for (const entry of entries) {
		if (entry.type === 'folder' && required.includes(entry.id)) {
			return true;
		}
	}

	return false;
}

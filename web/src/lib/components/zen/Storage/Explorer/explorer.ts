/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { fileExplorerDownloadQuery } from '$lib/api/system/file-explorer';
import type { APIResponse } from '$lib/types/common';
import type { FileNode } from '$lib/types/system/file-explorer';
import { formatBytesBinary } from '$lib/utils/bytes';
import { getAPIErrorText } from '$lib/utils/http';
import { getFileIcon } from '$lib/utils/icons';

export interface PathSegment {
	name: string;
	path: string;
}

export function entryName(path: string): string {
	return path === '/' ? '/' : path.slice(path.lastIndexOf('/') + 1);
}

export function parentPath(path: string): string {
	const index = path.lastIndexOf('/');
	return index <= 0 ? '/' : path.slice(0, index);
}

export function childPath(directory: string, name: string): string {
	return directory === '/' ? `/${name}` : `${directory}/${name}`;
}

export function normalizePath(input: string | null | undefined): string | null {
	const value = input?.trim() ?? '';
	if (!value.startsWith('/')) return null;
	const parts: string[] = [];
	for (const part of value.split('/')) {
		if (!part || part === '.') continue;
		if (part === '..') parts.pop();
		else parts.push(part);
	}
	return `/${parts.join('/')}`;
}

export function pathSegments(path: string): PathSegment[] {
	const parts = path.split('/').filter(Boolean);
	return parts.map((name, index) => ({ name, path: `/${parts.slice(0, index + 1).join('/')}` }));
}

export function isFolder(node: FileNode): boolean {
	return node.type === 'folder';
}

export function canDownload(node: FileNode): boolean {
	return !isFolder(node) && !node.special && !node.broken;
}

export function compareEntries(a: FileNode, b: FileNode): number {
	if (isFolder(a) !== isFolder(b)) return isFolder(a) ? -1 : 1;
	return entryName(a.id).localeCompare(entryName(b.id), undefined, {
		numeric: true,
		sensitivity: 'base'
	});
}

export function entryIcon(node: FileNode): string {
	if (isFolder(node)) {
		if (node.link) return 'icon-[lucide--folder-symlink]';
		if (node.mount) return 'icon-[lucide--hard-drive]';
		return 'icon-[lucide--folder]';
	}
	if (node.link) return 'icon-[lucide--file-symlink]';
	if (node.special) return 'icon-[lucide--file-cog]';
	return getFileIcon(entryName(node.id));
}

const specialLabels: Record<string, string> = {
	fifo: 'FIFO',
	socket: 'Socket',
	device: 'Device'
};

export function specialLabel(node: FileNode): string {
	return node.special ? (specialLabels[node.special] ?? node.special) : '';
}

export function tileDetail(node: FileNode): string {
	if (node.broken) return 'Broken Link';
	if (isFolder(node)) return '';
	if (node.special) return specialLabel(node);
	return formatBytesBinary(node.size ?? 0);
}

export function tileTitle(node: FileNode): string {
	return [
		node.id,
		node.mount ? `${node.mount} is mounted here` : '',
		node.link ? `Points to ${node.link}${node.broken ? ', which is missing' : ''}` : '',
		`Modified ${node.date.toLocaleString()}`
	]
		.filter(Boolean)
		.join('\n');
}

export function typeLabel(node: FileNode): string {
	if (node.broken) return 'Broken Symbolic Link';
	const base = isFolder(node) ? 'Folder' : node.special ? specialLabel(node) : 'File';
	return node.link ? `Symbolic Link to ${base}` : base;
}

const specialNouns: Record<string, string> = {
	fifo: 'a FIFO (named pipe)',
	socket: 'a socket',
	device: 'a device'
};

export function downloadBlock(node: FileNode): string {
	if (node.broken) return 'The link’s target is missing.';
	if (node.special)
		return `It’s ${specialNouns[node.special] ?? 'a special file'}, not a regular file.`;
	return '';
}

export function protectionReason(reason: string | undefined, mount?: string): string {
	switch (reason) {
		case undefined:
		case '':
			return '';
		case 'root':
			return 'The root folder can’t be changed.';
		case 'system_folder':
			return 'Items directly under / belong to the operating system.';
		case 'base_system':
			return 'It’s part of the FreeBSD base system.';
		case 'mount_point':
			return mount ? `${mount} is mounted here.` : 'Another file system is mounted here.';
		case 'contains_mount_point':
			return 'Another file system is mounted inside it.';
		case 'jail_root':
			return 'It’s the root folder of a jail.';
		case 'sylve_data':
			return 'Sylve keeps its own data here.';
		case 'contains_sylve_data':
			return 'Sylve’s own data is inside it.';
		default:
			return 'It’s protected.';
	}
}

export function changeBlock(node: FileNode): string {
	return protectionReason(node.protected, node.mount);
}

export const BATCH_LIMIT = 512;

export const DOWNLOAD_URL_LIMIT = 8000;

export type DownloadPlan = { kind: 'file' | 'archive'; ids: string[]; filename: string };

function archiveBase(path: string): string {
	return path === '/' ? 'root' : entryName(path);
}

export function downloadPlan(nodes: FileNode[]): DownloadPlan {
	const ids = nodes.map((node) => node.id);
	if (nodes.length !== 1) {
		const parent = archiveBase(parentPath(ids[0]));
		return { kind: 'archive', ids, filename: `${parent} - ${nodes.length} items.zip` };
	}
	return isFolder(nodes[0])
		? { kind: 'archive', ids, filename: `${archiveBase(ids[0])}.zip` }
		: { kind: 'file', ids, filename: entryName(ids[0]) };
}

export function downloadSelectionBlock(nodes: FileNode[]): string {
	if (nodes.length === 1) return isFolder(nodes[0]) ? '' : downloadBlock(nodes[0]);
	if (nodes.length > BATCH_LIMIT) return `Select ${BATCH_LIMIT} items or fewer.`;
	const query = fileExplorerDownloadQuery(nodes.map((node) => node.id));
	if (query.toString().length > DOWNLOAD_URL_LIMIT)
		return 'Their paths are too long to download together. Select fewer items, or download the folder they’re in as a zip.';
	return '';
}

export function hasContents(node: FileNode): boolean {
	return isFolder(node) && !node.link;
}

export function selectionBlock(nodes: FileNode[]): string {
	if (nodes.length > BATCH_LIMIT) return `Select ${BATCH_LIMIT} items or fewer.`;
	const protectedCount = nodes.filter((node) => changeBlock(node)).length;
	if (protectedCount === 0) return '';
	if (nodes.length === 1) return changeBlock(nodes[0]);
	return protectedCount === 1
		? 'One of the selected items is protected. Deselect it first.'
		: `${protectedCount} of the selected items are protected. Deselect them first.`;
}

export function transferProblem(
	sources: string[],
	destination: string,
	taken: Set<string>,
	move: boolean
): string {
	const verb = move ? 'move' : 'copy';
	const inside = sources.find(
		(source) => destination === source || destination.startsWith(`${source}/`)
	);
	if (inside) return `Can’t ${verb} ${entryName(inside)} into itself`;
	if (sources.some((source) => parentPath(source) === destination))
		return sources.length === 1
			? 'It’s already in this folder'
			: 'Some of the items are already in this folder';
	const clash = sources.find((source) => taken.has(entryName(source)));
	if (clash) return `${entryName(clash)} already exists here`;
	return '';
}

export function completedItems(response: Pick<APIResponse, 'data'>): number {
	const completed = (response.data as { completed?: unknown } | null | undefined)?.completed;
	return Array.isArray(completed) ? completed.length : 0;
}

export function partialMessage(done: number, total: number, verb: string, message: string): string {
	return done > 0
		? `${done} of ${total} items were ${verb} before this error. ${message}`
		: message;
}

export function transferErrorMessage(
	response: Pick<APIResponse, 'error' | 'message' | 'data'>,
	move: boolean,
	destination: string
): string {
	const text = getAPIErrorText(response, '');
	if (/file_explorer_move_incomplete/.test(text))
		return `Everything was copied to ${destination}, but some of the originals couldn’t be removed. Check both folders.`;
	if (/file_explorer_batch_conflict/.test(text))
		return 'Some of the selected items overlap or would end up with the same name.';
	if (/file_explorer_invalid_operation/.test(text))
		return `A folder can’t be ${move ? 'moved' : 'copied'} into itself.`;
	if (/file_explorer_unsupported_file_type/.test(text))
		return 'It contains a FIFO, socket or device, which can’t be copied.';
	if (/file_explorer_batch_too_large/.test(text))
		return `${move ? 'Move' : 'Copy'} up to ${BATCH_LIMIT} items at a time.`;
	return explorerErrorMessage(
		response,
		move ? 'The items could not be moved.' : 'The items could not be copied.'
	);
}

export function permissionString(node: FileNode): string {
	const mode = node.mode ? parseInt(node.mode, 8) : NaN;
	if (!Number.isFinite(mode)) return '';
	const kind = isFolder(node)
		? 'd'
		: node.special === 'fifo'
			? 'p'
			: node.special === 'socket'
				? 's'
				: node.special === 'device'
					? 'c'
					: '-';
	const triplet = (shift: number, special: boolean, mark: string) => {
		const execute = (mode & (1 << shift)) !== 0;
		return (
			(mode & (4 << shift) ? 'r' : '-') +
			(mode & (2 << shift) ? 'w' : '-') +
			(special ? (execute ? mark : mark.toUpperCase()) : execute ? 'x' : '-')
		);
	};
	return (
		kind +
		triplet(6, (mode & 0o4000) !== 0, 's') +
		triplet(3, (mode & 0o2000) !== 0, 's') +
		triplet(0, (mode & 0o1000) !== 0, 't')
	);
}

export function ownerLabel(node: FileNode): string {
	if (!node.owner && !node.group) return '';
	return `${node.owner ?? '?'}:${node.group ?? '?'}`;
}

export function nameProblem(name: string, taken: Set<string>, current?: string): string {
	const value = name.trim();
	if (!value) return 'Enter a name';
	if (value === '.' || value === '..') return 'Not a valid name';
	if (value.includes('/')) return 'Can’t contain /';
	if (/\p{Cc}/u.test(value)) return 'Can’t contain control characters';
	if (new TextEncoder().encode(value).length > 255) return 'Must be 255 bytes or fewer';
	if (value !== current && taken.has(value)) return 'Something with this name already exists here';
	return '';
}

const errorMessages: [RegExp, string][] = [
	[/file_explorer_entry_already_exists/, 'Something with that name already exists here.'],
	[/file_explorer_invalid_name/, 'That name isn’t allowed.'],
	[/file_explorer_name_too_long/, 'The name or path is too long.'],
	[
		/file_explorer_permission_denied/,
		'Permission was denied. The file system may be read only, or file flags may lock the item.'
	],
	[/file_explorer_entry_not_found/, 'It no longer exists. Refresh to see the current contents.'],
	[/file_explorer_not_directory/, 'That path isn’t a folder.'],
	[/file_explorer_root_mutation_forbidden/, 'The root folder can’t be changed.'],
	[/restore_in_progress/, 'A jail restore is running there. Try again when it finishes.'],
	[/file_explorer_busy/, 'It’s busy. Something may still be using it.'],
	[/file_explorer_no_space/, 'There isn’t enough free space.'],
	[/superuser_required/, 'Only administrators can change files.']
];

export function explorerErrorMessage(
	response: Pick<APIResponse, 'error' | 'message' | 'data'>,
	fallback: string
): string {
	const text = getAPIErrorText(response, fallback);
	if (/file_explorer_protected_path/.test(text)) {
		const data = response.data as { reason?: string } | null | undefined;
		return protectionReason(data?.reason || 'protected');
	}
	return errorMessages.find(([pattern]) => pattern.test(text))?.[1] ?? fallback;
}

const uploadMessages: Record<string, string> = {
	file_exists: 'A file with this name already exists here',
	invalid_filename: 'This file name isn’t allowed',
	restore_in_progress: 'A jail restore is running in this folder',
	insufficient_storage: 'There isn’t enough free space',
	upload_permission_denied: 'Permission was denied',
	destination_not_found: 'This folder no longer exists',
	invalid_destination: 'This folder can’t receive uploads',
	upload_capacity_exhausted: 'Too many uploads at once. Retry in a moment',
	superuser_required: 'Only administrators can upload files'
};

export function uploadErrorMessage(response: string): string {
	try {
		const parsed = JSON.parse(response) as {
			message?: unknown;
			data?: { code?: unknown; limitBytes?: unknown } | null;
		};
		const code =
			typeof parsed.data?.code === 'string'
				? parsed.data.code
				: typeof parsed.message === 'string'
					? parsed.message
					: '';
		const limit = Number(parsed.data?.limitBytes);
		if ((code === 'file_too_large' || code === 'request_too_large') && limit > 0)
			return `Larger than the ${formatBytesBinary(limit)} upload limit`;
		if (uploadMessages[code]) return uploadMessages[code];
	} catch {
		return 'The upload failed';
	}
	return 'The upload failed';
}

export function browseErrorMessage(response: Pick<APIResponse, 'error' | 'message' | 'data'>) {
	const text = getAPIErrorText(response, '');
	if (/file_explorer_entry_not_found/.test(text)) return 'This folder doesn’t exist.';
	if (/file_explorer_not_directory/.test(text)) return 'This path is a file, not a folder.';
	if (/file_explorer_permission_denied/.test(text)) return 'This folder can’t be opened.';
	if (/file_explorer_invalid_path/.test(text)) return 'This isn’t a valid path.';
	if (/superuser_required/.test(text)) return 'Only administrators can browse files.';
	return explorerErrorMessage(response, 'This folder could not be loaded.');
}

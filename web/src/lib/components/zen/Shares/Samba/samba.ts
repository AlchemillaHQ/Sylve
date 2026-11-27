/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { APIResponse } from '$lib/types/common';
import type {
	SambaShare,
	SambaStatus,
	ShareDataset,
	SharePermissionJob
} from '$lib/types/samba/shares';
import { getAPIErrorText, isAPIResponse } from '$lib/utils/http';

export type SharesData = {
	hostname: string;
	shares: SambaShare[] | APIResponse;
	fresh: boolean;
	status: SambaStatus | APIResponse | null;
	statusFresh: boolean;
};

export function cachedStatus(status: SambaStatus | APIResponse | null): SambaStatus | undefined {
	return status && !isAPIResponse(status) ? status : undefined;
}

export type AccessMode = 'signed-in' | 'guest-read' | 'guest-write';

export type PrincipalOption = {
	key: string;
	label: string;
	kind: 'user' | 'group';
	directory: boolean;
	warning?: string;
};

export const MAX_SHARE_NAME_LENGTH = 80;
const FORBIDDEN_NAME_CHARACTERS = '\\/:*?"<>|[],';
const RESERVED_NAMES = new Set(['global', 'homes', 'printers', 'print$', 'ipc$']);

export const AUDIT_OPERATIONS = [
	{ value: 'connect', label: 'Connect', icon: 'icon-[mdi--lan-connect]' },
	{ value: 'disconnect', label: 'Disconnect', icon: 'icon-[mdi--lan-disconnect]' },
	{ value: 'create_file', label: 'Create File', icon: 'icon-[mdi--file-plus]' },
	{ value: 'mkdirat', label: 'Create Folder', icon: 'icon-[mdi--folder-plus-outline]' },
	{ value: 'unlinkat', label: 'Delete', icon: 'icon-[mdi--delete-outline]' },
	{ value: 'renameat', label: 'Rename', icon: 'icon-[mdi--rename-box-outline]' },
	{ value: 'openat', label: 'Open', icon: 'icon-[mdi--file-lock-open-outline]' },
	{ value: 'close', label: 'Close', icon: 'icon-[mdi--file-lock-outline]' },
	{ value: 'read', label: 'Read', icon: 'icon-[mdi--file-eye-outline]' },
	{ value: 'write', label: 'Write', icon: 'icon-[mdi--file-edit-outline]' }
] as const;

export function auditOperationLabel(value: string): string {
	return AUDIT_OPERATIONS.find((operation) => operation.value === value)?.label ?? value;
}

const DATASET_BLOCKS: Record<string, string> = {
	root: 'It’s the root file system',
	system_path: 'It’s mounted on a system path',
	boot_environment: 'It’s a boot environment',
	sylve_data: 'It holds Sylve’s own data',
	contains_protected_data: 'It contains Sylve’s data or a boot environment'
};

export function datasetBlockReason(code: string | undefined): string {
	if (!code) return '';
	return DATASET_BLOCKS[code] ?? 'It can’t be shared';
}

export function datasetChoiceBlock(dataset: ShareDataset, shareId: number | undefined): string {
	if (dataset.sharedBy && dataset.sharedBy.id !== shareId) {
		return `Already shared as ${dataset.sharedBy.name}`;
	}
	if (dataset.block) return datasetBlockReason(dataset.block);
	if (!dataset.mounted || !dataset.mountpoint.startsWith('/')) return 'It isn’t mounted';
	return '';
}

const SHARE_STATES: Record<string, { label: string; tone: 'good' | 'muted' | 'warning' }> = {
	active: { label: 'Active', tone: 'good' },
	disabled: { label: 'Disabled', tone: 'muted' },
	dataset_missing: { label: 'Dataset Missing', tone: 'warning' },
	dataset_unmounted: { label: 'Not Mounted', tone: 'warning' }
};

export function shareState(share: SambaShare): {
	label: string;
	tone: 'good' | 'muted' | 'warning';
} {
	return (
		SHARE_STATES[share.state ?? (share.enabled ? 'active' : 'disabled')] ?? {
			label: 'Unknown',
			tone: 'muted'
		}
	);
}

const JOB_VISIBLE_FOR = 60 * 60 * 1000;

type JobSummary = {
	label: string;
	detail: string;
	tone: 'busy' | 'good' | 'muted' | 'warning';
};

function items(count: number): string {
	return `${count.toLocaleString()} item${count === 1 ? '' : 's'}`;
}

export function jobSummary(job: SharePermissionJob | undefined, now: number): JobSummary | null {
	if (!job) return null;
	if (job.state === 'running') {
		return {
			label: 'Applying Access',
			detail: `${items(job.processed)} updated so far`,
			tone: 'busy'
		};
	}
	const finished = job.finishedAt ? Date.parse(job.finishedAt) : NaN;
	if (Number.isFinite(finished) && now - finished > JOB_VISIBLE_FOR) return null;
	if (job.state === 'failed') {
		return {
			label: 'Access Not Applied',
			detail: job.error ? `Applying access to existing files failed: ${job.error}` : '',
			tone: 'warning'
		};
	}
	if (job.state === 'cancelled') {
		return {
			label: 'Access Partly Applied',
			detail: `Stopped after ${items(job.processed)}`,
			tone: 'muted'
		};
	}
	if (job.failed > 0) {
		return {
			label: 'Access Partly Applied',
			detail: `${items(job.failed)} of ${items(job.processed)} couldn’t be changed${job.error ? `: ${job.error}` : ''}`,
			tone: 'warning'
		};
	}
	return { label: 'Access Applied', detail: `Updated ${items(job.processed)}`, tone: 'good' };
}

export function enableBlock(share: SambaShare): string {
	if (share.enabled) return '';
	if (!share.datasetName) return 'Its dataset no longer exists';
	if (share.datasetBlock) {
		const reason = datasetBlockReason(share.datasetBlock);
		return `Its dataset can’t be shared: ${reason.charAt(0).toLowerCase()}${reason.slice(1)}`;
	}
	return '';
}

export function accessMode(share: Pick<SambaShare, 'guest'>): AccessMode {
	if (!share.guest.enabled) return 'signed-in';
	return share.guest.writeable ? 'guest-write' : 'guest-read';
}

export function plural(count: number, word: string): string {
	return `${count} ${word}${count === 1 ? '' : 's'}`;
}

export function accessSummary(share: SambaShare): string {
	const mode = accessMode(share);
	if (mode === 'guest-read') return 'Guests · Read Only';
	if (mode === 'guest-write') return 'Guests · Read & Write';
	let users = 0;
	let groups = 0;
	for (const access of ['read', 'write'] as const) {
		const set = share.permissions[access];
		users += set.users.length + set.directory.filter((entry) => entry.kind === 'user').length;
		groups += set.groups.length + set.directory.filter((entry) => entry.kind === 'group').length;
	}
	const parts = [users ? plural(users, 'user') : '', groups ? plural(groups, 'group') : ''];
	return parts.filter(Boolean).join(', ') || 'Nobody';
}

export function connectionsByShare(status: SambaStatus | undefined): Map<string, number> {
	const counts = new Map<string, number>();
	for (const connection of status?.connections ?? []) {
		const key = connection.share.toLowerCase();
		counts.set(key, (counts.get(key) ?? 0) + 1);
	}
	return counts;
}

export function nameProblem(name: string, others: SambaShare[]): string {
	const trimmed = name.trim();
	if (!trimmed) return 'Enter a name';
	if ([...trimmed].length > MAX_SHARE_NAME_LENGTH)
		return `Use ${MAX_SHARE_NAME_LENGTH} characters or fewer`;
	if (
		[...trimmed].some(
			(char) => char < ' ' || char === '\x7f' || FORBIDDEN_NAME_CHARACTERS.includes(char)
		)
	)
		return 'Remove \\ / : * ? " < > | [ ] and commas';
	if (RESERVED_NAMES.has(trimmed.toLowerCase())) return 'This name is reserved by Samba';
	const lower = trimmed.toLowerCase();
	if (others.some((share) => share.name.toLowerCase() === lower))
		return 'Another share uses this name';
	return '';
}

export function maskProblem(mask: string): string {
	return /^[0-7]{4}$/.test(mask.trim()) ? '' : 'Use four octal digits, like 0664';
}

export function wholeNumberProblem(value: string, message: string): string {
	return /^\d+$/.test(value.trim()) ? '' : message;
}

export function extraConfigProblem(config: string): string {
	if (config.includes('\0')) return 'Remove the null character';
	if (config.split(/\r?\n|\r/).some((line) => line.trimStart().startsWith('[')))
		return 'Remove section headers like [name]';
	return '';
}

const DIRECTORY_BUSY =
	'Another Samba or Active Directory change is in progress. Try again in a moment.';

export function errorCode(response: Pick<APIResponse, 'error'>): string {
	return typeof response.error === 'string' ? response.error.trim() : '';
}

const SAVE_ERRORS: Record<string, string> = {
	share_with_name_exists: 'Another share uses this name.',
	share_with_dataset_exists: 'Another share already uses this dataset.',
	share_name_reserved: 'This name is reserved by Samba.',
	share_name_too_long: `Share names can be at most ${MAX_SHARE_NAME_LENGTH} characters.`,
	invalid_share_name: 'The name contains characters Samba doesn’t allow.',
	dataset_not_found: 'The dataset no longer exists.',
	dataset_not_mounted: 'The dataset isn’t mounted.',
	no_principals_selected_and_guests_not_allowed:
		'Choose at least one user or group, or allow guests.',
	guest_only_share_cannot_have_principals: 'A guest share can’t also list users or groups.',
	directory_service_operation_in_progress: DIRECTORY_BUSY,
	directory_service_unavailable: 'Active Directory couldn’t be reached.'
};

export function shareSaveError(response: Pick<APIResponse, 'error' | 'message'>): string {
	const code = errorCode(response);
	if (response.message === 'samba_apply_failed')
		return 'The share was saved, but Samba couldn’t reload it. Check that Samba is running.';
	if (code.startsWith('dataset_not_shareable')) {
		const reason = code.split(':')[1]?.trim();
		return `This dataset can’t be shared. ${datasetBlockReason(reason)}.`;
	}
	if (code.startsWith('user_not_pam'))
		return 'Only system (PAM) users can sign in to Samba shares.';
	if (code.startsWith('invalid_samba_share_config'))
		return 'Samba rejected the extra configuration. Check it and try again.';
	for (const [key, message] of Object.entries(SAVE_ERRORS))
		if (code.startsWith(key)) return message;
	return getAPIErrorText(response, 'The share couldn’t be saved.');
}

export function shareDeleteError(response: Pick<APIResponse, 'error' | 'message'>): string {
	if (errorCode(response).startsWith('directory_service_operation_in_progress'))
		return DIRECTORY_BUSY;
	return getAPIErrorText(response, 'It couldn’t be deleted.');
}

export function workgroupProblem(value: string): string {
	const trimmed = value.trim();
	if (!trimmed) return 'Enter a workgroup';
	if (trimmed.length > 15) return 'Use 15 characters or fewer';
	if (!/^[A-Za-z0-9_-]+$/.test(trimmed)) return 'Use letters, digits, hyphens or underscores';
	if (trimmed.startsWith('-')) return 'Don’t start with a hyphen';
	return '';
}

export function serverStringProblem(value: string): string {
	if (!value.trim()) return 'Enter a description';
	if (/[\r\n\0]/.test(value)) return 'Use a single line';
	if (new TextEncoder().encode(value).length > 100) return 'Use 100 bytes or fewer';
	if (value.endsWith('\\')) return 'Don’t end with a backslash';
	return '';
}

export function charsetProblem(value: string): string {
	const trimmed = value.trim();
	if (!trimmed) return 'Enter a character set';
	return /^[A-Za-z0-9._:+-]+$/.test(trimmed) ? '' : 'Use a character set name like UTF-8';
}

export function globalConfigProblem(config: string): string {
	return config.includes('\0') ? 'Remove the null character' : '';
}

function testparmFindings(detail: string): string {
	const lines = detail
		.split('\n')
		.map((line) => line.trim())
		.filter((line) => /unknown|error|invalid|ignoring/i.test(line));
	return [...new Set(lines)].slice(0, 3).join(' ');
}

export function configSaveError(response: Pick<APIResponse, 'error' | 'message'>): string {
	const code = errorCode(response);
	if (response.message === 'samba_apply_failed')
		return 'The settings were saved, but Samba couldn’t restart. Check that Samba is running.';
	if (code.startsWith('directory_service_operation_in_progress')) return DIRECTORY_BUSY;
	if (code.startsWith('directory_service_entry_in_use'))
		return 'An interrupted Active Directory restore must be rolled back first.';
	if (code.startsWith('unsupported unixCharset'))
		return 'This node doesn’t support that character set.';
	if (code.startsWith('unknown interface:'))
		return `The interface ${code.slice('unknown interface:'.length).trim()} no longer exists.`;
	if (code.startsWith('samba_config_rejected')) {
		const findings = testparmFindings(code.slice('samba_config_rejected:'.length));
		return findings
			? `Samba rejected the configuration: ${findings}`
			: 'Samba rejected the configuration. Check the extra configuration.';
	}
	if (code.startsWith('samba_not_initialized'))
		return 'Samba hasn’t been set up on this node. Turn on the Samba service in Services.';
	return getAPIErrorText(response, 'The settings couldn’t be saved.');
}

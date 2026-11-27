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
import {
	NFSValidationErrorSchema,
	type NFSConfig,
	type NFSDataset,
	type NFSShare,
	type NFSStatus
} from '$lib/types/nfs';
import { getAPIErrorText } from '$lib/utils/http';
import { Address4, Address6 } from 'ip-address';
import { blockedDatasetReason, datasetBlockReason, errorCode } from '../shared';

export type SharesData = {
	hostname: string;
	shares: NFSShare[] | APIResponse;
	fresh: boolean;
	status: NFSStatus | APIResponse | null;
	statusFresh: boolean;
};

export type SettingsData = {
	hostname: string;
	config: NFSConfig | APIResponse | null;
	fresh: boolean;
	status: NFSStatus | APIResponse | null;
	statusFresh: boolean;
};

export type AddressOption = { value: string; label: string };
export type Security = NFSShare['security'][number];
export type MappingMode = 'squash' | 'root' | 'all';
type Tone = 'good' | 'muted' | 'warning' | 'busy';

export const SECURITY_FLAVORS: { value: Security; label: string; description: string }[] = [
	{ value: 'sys', label: 'SYS', description: 'Trusts the user and group IDs clients send' },
	{ value: 'krb5', label: 'KRB5', description: 'Kerberos sign-in' },
	{ value: 'krb5i', label: 'KRB5I', description: 'Kerberos with integrity checks' },
	{ value: 'krb5p', label: 'KRB5P', description: 'Kerberos with encryption' }
];

export function shareTitle(share: NFSShare): string {
	return share.datasetName || share.dataset;
}

export function mappingMode(share: Pick<NFSShare, 'maprootUser' | 'mapallUser'>): MappingMode {
	if (share.mapallUser) return 'all';
	return share.maprootUser ? 'root' : 'squash';
}

function account(user: string, group: string): string {
	return group ? `${user}:${group}` : user;
}

export function mappingSummary(share: NFSShare): string {
	if (share.mapallUser) return `Everyone as ${account(share.mapallUser, share.mapallGroup)}`;
	if (share.maprootUser) return `Root as ${account(share.maprootUser, share.maprootGroup)}`;
	return 'Root squashed';
}

export function clientsSummary(share: NFSShare): string {
	if (share.everyone) return 'Everyone';
	if (share.clients.length <= 2) return share.clients.join(', ');
	return `${share.clients.slice(0, 2).join(', ')}, +${share.clients.length - 2} more`;
}

export function clientProblem(value: string): string {
	if (value.includes('%')) return 'Scoped addresses aren’t supported';
	let address: Address4 | Address6;
	try {
		address = value.includes(':') ? new Address6(value) : new Address4(value);
	} catch {
		return 'Use an IP address or a network like 192.168.1.0/24';
	}
	if (address instanceof Address6 && address.isMapped4()) return 'Use the plain IPv4 address';
	if (address.isMulticast()) return 'Multicast addresses can’t be clients';
	if (!value.includes('/')) {
		return address.isUnspecified() ? 'Choose Everyone instead of an unspecified address' : '';
	}
	return address.subnetMask === 0 ? 'Choose Everyone instead of a /0 network' : '';
}

export function normalizedAddress(value: string): string {
	try {
		return (value.includes(':') ? new Address6(value) : new Address4(value)).correctForm();
	} catch {
		return value.trim();
	}
}

export function bindAddressProblem(value: string): string {
	if (value.includes('/') || value.includes('%')) return 'Use a single address without a zone';
	try {
		const address = value.includes(':') ? new Address6(value) : new Address4(value);
		return address.isUnspecified() ||
			address.isMulticast() ||
			address.isLinkLocal() ||
			address.isBroadcast() ||
			(address instanceof Address6 && address.isMapped4())
			? 'Use a unicast address on this node'
			: '';
	} catch {
		return 'Use an IPv4 or IPv6 address';
	}
}

export function splitClients(text: string): string[] {
	return text
		.split(/[\s,]+/)
		.map((entry) => entry.trim())
		.filter(Boolean);
}

const ACCOUNT_NAME = /^[^-:"[\]%/\p{Cc}][^:"[\]%/\p{Cc}]*$/u;

export function accountProblem(name: string): string {
	const trimmed = name.trim();
	if (!trimmed) return '';
	return new TextEncoder().encode(trimmed).length > 512 || !ACCOUNT_NAME.test(trimmed)
		? 'Use a valid name'
		: '';
}

export function securityLabel(flavor: Security): string {
	return SECURITY_FLAVORS.find((entry) => entry.value === flavor)?.label ?? flavor;
}

function exported(share: NFSShare, status: NFSStatus | undefined): boolean {
	return Boolean(
		status?.applyStatus === 'applied' &&
		status.filesystems.some((filesystem) => filesystem.guid === share.dataset)
	);
}

export function shareState(
	share: NFSShare,
	status: NFSStatus | undefined
): { label: string; tone: Tone; detail?: string } {
	if (!share.enabled) return { label: 'Disabled', tone: 'muted' };
	if (share.state === 'dataset_missing') {
		return {
			label: 'Dataset Missing',
			tone: 'warning',
			detail: 'Its dataset no longer exists, so it isn’t exported'
		};
	}
	if (share.state === 'dataset_unmounted') {
		return {
			label: 'Not Mounted',
			tone: 'warning',
			detail: 'Its dataset isn’t mounted, so it isn’t exported'
		};
	}
	if (!status) return { label: 'Enabled', tone: 'muted' };
	if (!status.runtimeAvailable) {
		return { label: 'Inactive', tone: 'muted', detail: 'NFS needs Sylve to run as root' };
	}
	if (!status.enabled) {
		return { label: 'Inactive', tone: 'muted', detail: 'The NFS service is turned off' };
	}
	if (status.applyStatus === 'applying' || status.applyStatus === 'starting') {
		return { label: 'Applying', tone: 'busy' };
	}
	if (exported(share, status)) return { label: 'Exported', tone: 'good' };
	return {
		label: 'Not Exported',
		tone: 'warning',
		detail: status.lastError || 'NFS hasn’t applied this share yet'
	};
}

const DATASET_REASONS: Record<string, string> = {
	dataset_missing: 'It no longer exists',
	dataset_not_in_usable_pool: 'Its pool isn’t used by Sylve',
	dataset_not_mounted: 'It isn’t mounted or unlocked',
	dataset_sharenfs_set: 'Its sharenfs property is on',
	dataset_mount_mismatch: 'Its mountpoint belongs to another file system',
	invalid_mountpoint: 'It has no usable mountpoint'
};

function datasetReason(code: string | undefined, block?: string, fallback = ''): string {
	if (code === 'dataset_not_shareable') return datasetBlockReason(block);
	return (code && DATASET_REASONS[code]) || fallback;
}

export function datasetChoiceReason(dataset: NFSDataset): string {
	if (dataset.available) return '';
	return datasetReason(dataset.code, dataset.block, 'It can’t be exported');
}

const FIELD_MESSAGES: Record<string, string> = {
	invalid_dataset: 'Choose a dataset',
	too_many_shares: 'At most 256 shares can be saved',
	invalid_description: 'Use at most 256 bytes without control characters',
	clients_required: 'Add an address, or choose Everyone',
	too_many_clients: 'Use at most 64 addresses',
	client_too_long: 'Addresses can be at most 64 characters',
	invalid_client: 'Use IP addresses or networks like 192.168.1.0/24',
	client_matches_everyone: 'Choose Everyone instead of a /0 network',
	everyone_with_clients: 'Everyone can’t be combined with addresses',
	clients_overlap: 'Some addresses overlap',
	everyone_rule_overlap: 'Another enabled share of this dataset already has clients',
	client_rules_overlap: 'Another enabled share of this dataset covers some of these addresses',
	security_required: 'Choose at least one',
	invalid_security: 'Choose SYS or a Kerberos type',
	kerberos_principal_required: 'Choose a Kerberos principal in NFS settings first',
	kerberos_requires_names: 'Turn off numeric IDs in NFS settings first',
	kerberos_credentials_unavailable: 'The Kerberos principal’s keys aren’t installed',
	kerberos_credentials_error: 'The Kerberos keys couldn’t be read',
	directory_services_unavailable: 'Directory Services isn’t available',
	invalid_account_name: 'Use a valid name',
	mapping_group_without_user: 'Choose a user too',
	maproot_with_mapall: 'Choose one kind of mapping',
	mapping_unresolved: 'This user or group doesn’t exist on this node',
	invalid_protocol: 'Choose NFSv3, NFSv4 or both',
	invalid_v4_max_version: 'Choose NFSv4.0, 4.1 or 4.2',
	udp_requires_nfsv3: 'UDP needs NFSv3',
	invalid_thread_range: 'Use 1 to 256, with the minimum not above the maximum',
	too_many_bind_addresses: 'Choose at most 32 addresses',
	invalid_bind_address: 'Use addresses without zones, not link-local',
	invalid_kerberos_principal: 'Use a principal like nfs/host@REALM'
};

export function enableBlock(share: NFSShare): string {
	return share.enabled || !share.datasetBlock
		? ''
		: blockedDatasetReason(share.datasetBlock, 'exported');
}

export function fieldError(
	response: APIResponse
): { field: string; code: string; message: string; mapped: boolean } | null {
	const parsed = NFSValidationErrorSchema.safeParse(response.data);
	if (!parsed.success) return null;
	const { field, code, block, message } = parsed.data;
	const mapped = datasetReason(code, block) || FIELD_MESSAGES[code];
	return { field, code, message: mapped || message.replace(/\.$/, ''), mapped: Boolean(mapped) };
}

export function applyPending(response: APIResponse): boolean {
	return response.message === 'nfs_configuration_saved_apply_pending';
}

export function pendingDetail(
	response: APIResponse,
	lead = 'Saved, but NFS couldn’t apply it'
): string {
	const detail = errorCode(response)
		.replace(/^nfs_configuration_saved_apply_pending:?\s*/, '')
		.split('\n')[0]
		.trim();
	return detail ? `${lead}: ${detail}` : `${lead}.`;
}

const MAPPING_CONTEXT = 'Its user mapping can’t be used';
const SECURITY_CONTEXT = 'Its security types can’t be used';
const FIELD_CONTEXTS: Record<string, string> = {
	dataset: 'Its dataset can’t be exported',
	clients: 'Its clients can’t be used',
	security: SECURITY_CONTEXT,
	kerberosPrincipal: SECURITY_CONTEXT,
	numericIds: SECURITY_CONTEXT,
	maprootUser: MAPPING_CONTEXT,
	maprootGroup: MAPPING_CONTEXT,
	mapallUser: MAPPING_CONTEXT,
	mapallGroup: MAPPING_CONTEXT
};

function sentence(text: string): string {
	return /[.!?]$/.test(text) ? text : `${text}.`;
}

export function requestError(response: APIResponse, fallback: string): string {
	const detail = fieldError(response);
	if (detail) {
		const context = detail.mapped ? FIELD_CONTEXTS[detail.field] : '';
		return context ? `${context}. ${sentence(detail.message)}` : sentence(detail.message);
	}
	if (errorCode(response) === 'nfs_share_not_found') return 'The share no longer exists.';
	return getAPIErrorText(response, fallback);
}

export function planError(response: APIResponse, fallback: string): string {
	const parsed = NFSValidationErrorSchema.safeParse(response.data);
	return parsed.success ? sentence(parsed.data.message) : requestError(response, fallback);
}

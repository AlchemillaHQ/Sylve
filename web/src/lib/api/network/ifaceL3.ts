// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	HostInterfaceL3ListSchema,
	HostInterfaceL3PendingEntrySchema,
	type HostInterfaceL3List,
	type HostInterfaceL3PendingEntry
} from '$lib/types/network/ifaceL3';
import { apiRequest } from '$lib/utils/http';

export type HostInterfaceL3SavePayload = {
	ipv6Mode?: string;
	mtu?: number | null;
	metric?: number | null;
	addresses: { address: string }[];
	expectedRevision: number;
};

export async function getHostInterfaceL3(): Promise<HostInterfaceL3List | APIResponse> {
	return await apiRequest('/network/interface/l3', HostInterfaceL3ListSchema, 'GET', undefined, {
		preserveErrors: true
	});
}

export async function saveHostInterfaceL3(
	name: string,
	payload: HostInterfaceL3SavePayload
): Promise<HostInterfaceL3PendingEntry | APIResponse> {
	return await apiRequest(
		`/network/interface/${encodeURIComponent(name)}/l3`,
		HostInterfaceL3PendingEntrySchema,
		'PUT',
		payload,
		{ preserveErrors: true }
	);
}

export async function deleteHostInterfaceL3(
	name: string,
	expectedRevision: number
): Promise<HostInterfaceL3PendingEntry | APIResponse> {
	return await apiRequest(
		`/network/interface/${encodeURIComponent(name)}/l3?expectedRevision=${expectedRevision}`,
		HostInterfaceL3PendingEntrySchema,
		'DELETE',
		undefined,
		{ preserveErrors: true }
	);
}

export async function reapplyHostInterfaceL3(name: string): Promise<APIResponse> {
	return await apiRequest(
		`/network/interface/${encodeURIComponent(name)}/l3/reapply`,
		APIResponseSchema,
		'POST'
	);
}

export async function getHostInterfaceL3Pending(): Promise<
	HostInterfaceL3PendingEntry[] | APIResponse
> {
	return await apiRequest(
		'/network/interface/l3/pending',
		HostInterfaceL3PendingEntrySchema.array(),
		'GET',
		undefined,
		{ preserveErrors: true }
	);
}

export async function confirmHostInterfaceL3(id: string): Promise<APIResponse> {
	return await apiRequest(
		`/network/host-ip/pending/${encodeURIComponent(id)}/confirm`,
		APIResponseSchema,
		'POST'
	);
}

export async function revertHostInterfaceL3(id: string): Promise<APIResponse> {
	return await apiRequest(
		`/network/host-ip/pending/${encodeURIComponent(id)}/revert`,
		APIResponseSchema,
		'POST'
	);
}

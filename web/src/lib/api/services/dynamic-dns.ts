import { z } from 'zod/v4';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	DynamicDNSEntrySchema,
	type DynamicDNSEntry,
	type DynamicDNSEntryInput
} from '$lib/types/services/dynamic-dns';
import { apiRequest } from '$lib/utils/http';

export async function getDynamicDNSEntries(): Promise<DynamicDNSEntry[] | APIResponse> {
	return await apiRequest('/dynamic-dns/entries', z.array(DynamicDNSEntrySchema), 'GET');
}

export async function createDynamicDNSEntry(
	input: DynamicDNSEntryInput
): Promise<DynamicDNSEntry | APIResponse> {
	return await apiRequest('/dynamic-dns/entries', DynamicDNSEntrySchema, 'POST', input);
}

export async function updateDynamicDNSEntry(
	id: number,
	input: DynamicDNSEntryInput
): Promise<DynamicDNSEntry | APIResponse> {
	return await apiRequest(`/dynamic-dns/entries/${id}`, DynamicDNSEntrySchema, 'PUT', input);
}

export async function deleteDynamicDNSEntry(id: number): Promise<APIResponse> {
	return await apiRequest(`/dynamic-dns/entries/${id}`, APIResponseSchema, 'DELETE');
}

export async function syncDynamicDNSEntry(id: number): Promise<DynamicDNSEntry | APIResponse> {
	return await apiRequest(`/dynamic-dns/entries/${id}/sync`, DynamicDNSEntrySchema, 'POST');
}

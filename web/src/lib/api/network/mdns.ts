import { z } from 'zod/v4';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	MdnsSettingsSchema,
	type MdnsSettings,
	type MdnsSettingsRequest,
	MdnsRecordWithManagedSchema,
	type MdnsRecordWithManaged,
	type MdnsRecord
} from '$lib/types/network/mdns';
import { apiRequestData, apiRequestResult } from '$lib/utils/http';

export async function getMdnsSettings(): Promise<MdnsSettings> {
	return await apiRequestData('/mdns/config', MdnsSettingsSchema, 'GET');
}

export async function setMdnsSettings(config: MdnsSettingsRequest): Promise<APIResponse> {
	return await apiRequestResult('/mdns/config', APIResponseSchema, 'PUT', config);
}

export async function getMdnsRecords(): Promise<MdnsRecordWithManaged[]> {
	return await apiRequestData('/mdns/records', z.array(MdnsRecordWithManagedSchema), 'GET');
}

export async function createMdnsRecord(record: Partial<MdnsRecord>): Promise<APIResponse> {
	return await apiRequestResult('/mdns/records', APIResponseSchema, 'POST', record);
}

export async function updateMdnsRecord(
	id: number,
	record: Partial<MdnsRecord>
): Promise<APIResponse> {
	return await apiRequestResult(`/mdns/records/${id}`, APIResponseSchema, 'PUT', record);
}

export async function deleteMdnsRecord(id: number): Promise<APIResponse> {
	return await apiRequestResult(`/mdns/records/${id}`, APIResponseSchema, 'DELETE');
}

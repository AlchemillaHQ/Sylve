import { z } from 'zod/v4';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
    MdnsSettingsSchema,
    type MdnsSettings,
    MdnsRecordWithManagedSchema,
    type MdnsRecordWithManaged,
    type MdnsRecord
} from '$lib/types/network/mdns';
import { apiRequest } from '$lib/utils/http';

export async function getMdnsSettings(): Promise<MdnsSettings> {
    return await apiRequest('/mdns/config', MdnsSettingsSchema, 'GET');
}

export async function setMdnsSettings(config: Partial<MdnsSettings>): Promise<APIResponse> {
    return await apiRequest('/mdns/config', APIResponseSchema, 'POST', config);
}

export async function getMdnsRecords(): Promise<MdnsRecordWithManaged[]> {
    return await apiRequest('/mdns/records', z.array(MdnsRecordWithManagedSchema), 'GET');
}

export async function createMdnsRecord(record: Partial<MdnsRecord>): Promise<APIResponse> {
    return await apiRequest('/mdns/records', APIResponseSchema, 'POST', record);
}

export async function updateMdnsRecord(id: number, record: Partial<MdnsRecord>): Promise<APIResponse> {
    return await apiRequest(`/mdns/records/${id}`, APIResponseSchema, 'PUT', record);
}

export async function deleteMdnsRecord(id: number): Promise<APIResponse> {
    return await apiRequest(`/mdns/records/${id}`, APIResponseSchema, 'DELETE');
}

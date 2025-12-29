import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { BasicSettingsSchema, type BasicSettings } from '$lib/types/system/settings';
import { apiRequest } from '$lib/utils/http';
import { type AvailableService } from '../../types/system/settings';

export async function getBasicSettings(): Promise<BasicSettings> {
	return await apiRequest('/basic/settings', BasicSettingsSchema, 'GET');
}

export async function updateUsablePools(pools: string[]): Promise<APIResponse> {
	return apiRequest('/system/basic-settings/pools', APIResponseSchema, 'PUT', pools);
}

export async function toggleService(service: AvailableService): Promise<APIResponse> {
	return apiRequest(`/system/basic-settings/services/${service}/toggle`, APIResponseSchema, 'PUT');
}

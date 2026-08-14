import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { BasicSettingsSchema, type BasicSettings } from '$lib/types/system/settings';
import { apiRequest, isAPIResponse } from '$lib/utils/http';
import { type AvailableService } from '../../types/system/settings';

export async function getBasicSettings(hostname?: string): Promise<BasicSettings | APIResponse> {
	return await apiRequest(
		'/system/basic-settings',
		BasicSettingsSchema,
		'GET',
		undefined,
		hostname ? { hostname } : undefined
	);
}

export async function getLocalBasicSettings(): Promise<BasicSettings | APIResponse> {
	return await apiRequest('/basic/settings', BasicSettingsSchema, 'GET');
}

export function basicSettingsOrFallback(
	result: BasicSettings | APIResponse,
	fallback?: BasicSettings
): BasicSettings {
	if (!isAPIResponse(result)) return result;
	return fallback ?? { pools: [], services: [], initialized: false };
}

export async function updateUsablePools(pools: string[]): Promise<APIResponse> {
	return apiRequest('/system/basic-settings/pools', APIResponseSchema, 'PUT', pools);
}

export async function setServiceEnabled(
	service: AvailableService,
	enabled: boolean
): Promise<APIResponse> {
	return apiRequest(`/system/basic-settings/services/${service}`, APIResponseSchema, 'PATCH', {
		enabled
	});
}

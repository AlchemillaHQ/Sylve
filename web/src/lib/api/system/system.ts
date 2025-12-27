import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';

export async function rebootSystem(): Promise<APIResponse> {
	return apiRequest('/basic/system/reboot', APIResponseSchema, 'PUT');
}

export async function getBasicHealth(): Promise<APIResponse> {
	return await apiRequest('/health/basic', APIResponseSchema, 'GET');
}

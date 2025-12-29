import {
	InitializeSchema,
	type Initialize,
	BasicSettingsSchema,
	type BasicSettings
} from '$lib/types/basic';
import { apiRequest } from '$lib/utils/http';

export async function initialize(pools: string[], services: string[]): Promise<Initialize> {
	return await apiRequest('/basic/initialize', InitializeSchema, 'POST', {
		pools,
		services
	});
}

export async function getBasicSettings(): Promise<BasicSettings> {
	return await apiRequest('/basic/settings', BasicSettingsSchema, 'GET');
}

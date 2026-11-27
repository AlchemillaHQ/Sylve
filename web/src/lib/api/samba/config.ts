import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { SambaConfigSchema, type SambaConfig } from '$lib/types/samba/config';
import { apiRequestData, apiRequestResult, type NodeAPIDataRequestOptions } from '$lib/utils/http';
import type { SambaRequestOptions } from './share';

export type SambaConfigUpdate = Omit<SambaConfig, 'id'>;

export async function getSambaConfig(options?: NodeAPIDataRequestOptions): Promise<SambaConfig> {
	return await apiRequestData('/samba/config', SambaConfigSchema, 'GET', undefined, options);
}

export async function getSambaConfigResult(
	options: SambaRequestOptions
): Promise<SambaConfig | APIResponse> {
	return await apiRequestResult('/samba/config', SambaConfigSchema, 'GET', undefined, options);
}

export async function updateSambaConfig(
	config: SambaConfigUpdate,
	options?: SambaRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult('/samba/config', APIResponseSchema, 'PUT', config, options);
}

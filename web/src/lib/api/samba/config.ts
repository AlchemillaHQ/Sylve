import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { SambaConfigSchema, type SambaConfig } from '$lib/types/samba/config';
import { apiRequestData, apiRequestResult } from '$lib/utils/http';

export type SambaConfigUpdate = Omit<SambaConfig, 'id'>;

export async function getSambaConfig(): Promise<SambaConfig> {
	return await apiRequestData('/samba/config', SambaConfigSchema, 'GET');
}

export async function updateSambaConfig(config: SambaConfigUpdate): Promise<APIResponse> {
	return await apiRequestResult('/samba/config', APIResponseSchema, 'PUT', config);
}

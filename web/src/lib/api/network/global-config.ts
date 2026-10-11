import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	NetworkGlobalConfigSchema,
	type NetworkGlobalConfig,
	type NetworkGlobalConfigUpdate
} from '$lib/types/network/global-config';
import {
	apiRequestData,
	apiRequestResult,
	type NodeAPIDataRequestOptions,
	type NodeAPIRequestOptions
} from '$lib/utils/http';

export async function getNetworkGlobalConfig(
	options?: NodeAPIDataRequestOptions
): Promise<NetworkGlobalConfig> {
	return await apiRequestData(
		'/network/global-config',
		NetworkGlobalConfigSchema,
		'GET',
		undefined,
		options
	);
}

export async function updateNetworkGlobalConfig(
	config: NetworkGlobalConfigUpdate,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		'/network/global-config',
		APIResponseSchema,
		'PUT',
		config,
		options
	);
}

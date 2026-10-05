// SPDX-License-Identifier: BSD-2-Clause

import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { ISCSIConfigSchema, type ISCSIConfig } from '$lib/types/iscsi/config';
import { apiRequestResult, type NodeAPIRequestOptions } from '$lib/utils/http';

export async function getISCSIConfig(
	options: NodeAPIRequestOptions = {}
): Promise<ISCSIConfig | APIResponse> {
	return await apiRequestResult('/iscsi/config', ISCSIConfigSchema, 'GET', undefined, {
		...options,
		sensitive: true
	});
}

export async function updateISCSIConfig(
	extraTargetConfig: string,
	options: NodeAPIRequestOptions = {}
): Promise<APIResponse> {
	return await apiRequestResult(
		'/iscsi/config',
		APIResponseSchema,
		'PUT',
		{ extraTargetConfig },
		{ ...options, sensitive: true }
	);
}

import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';

export async function modifyRAM(
	ctId: number,
	bytes: number,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/jail/${ctId}/hardware/ram`,
		APIResponseSchema,
		'PUT',
		{ memory: bytes },
		{ ...options, preserveErrors: true }
	);
}

export async function modifyCPU(
	ctId: number,
	cores: number,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/jail/${ctId}/hardware/cpu`,
		APIResponseSchema,
		'PUT',
		{ cores: Math.trunc(cores) },
		{ ...options, preserveErrors: true }
	);
}

export async function updateResourceLimits(
	ctId: number,
	enabled: boolean,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/jail/${ctId}/hardware/resource-limits`,
		APIResponseSchema,
		'PUT',
		{ enabled },
		{ ...options, preserveErrors: true }
	);
}

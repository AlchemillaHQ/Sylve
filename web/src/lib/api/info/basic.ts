import type { APIResponse } from '$lib/types/common';
import { BasicInfoSchema, type BasicInfo } from '$lib/types/info/basic';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';

export async function getBasicInfo(): Promise<BasicInfo> {
	return await apiRequest('/info/basic', BasicInfoSchema, 'GET');
}

export async function getBasicInfoResult(
	options?: NodeAPIRequestOptions
): Promise<BasicInfo | APIResponse> {
	return await apiRequest('/info/basic', BasicInfoSchema, 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

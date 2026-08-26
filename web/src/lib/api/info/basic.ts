import type { APIResponse } from '$lib/types/common';
import { BasicInfoSchema, type BasicInfo } from '$lib/types/info/basic';
import { apiRequestData, apiRequestResult, type NodeAPIDataRequestOptions } from '$lib/utils/http';

export async function getBasicInfo(): Promise<BasicInfo> {
	return await apiRequestData('/info/basic', BasicInfoSchema, 'GET');
}

export async function getBasicInfoResult(
	options?: NodeAPIDataRequestOptions
): Promise<BasicInfo | APIResponse> {
	return await apiRequestResult('/info/basic', BasicInfoSchema, 'GET', undefined, options);
}

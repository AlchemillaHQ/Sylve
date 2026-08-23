import {
	CPUInfoHistoricalSchema,
	CPUInfoSchema,
	type CPUInfo,
	type CPUInfoHistorical
} from '$lib/types/info/cpu';
import type { APIResponse } from '$lib/types/common';
import { apiRequestData, apiRequestResult, type NodeAPIDataRequestOptions } from '$lib/utils/http';

export async function getCPUInfo(): Promise<CPUInfo>;
export async function getCPUInfo(
	queryType: 'current',
	options?: NodeAPIDataRequestOptions
): Promise<CPUInfo>;
export async function getCPUInfo(
	queryType: 'historical',
	options?: NodeAPIDataRequestOptions
): Promise<CPUInfoHistorical>;
export async function getCPUInfo(
	queryType?: 'current' | 'historical',
	options?: NodeAPIDataRequestOptions
): Promise<CPUInfo | CPUInfoHistorical> {
	if (queryType === 'historical') {
		return await apiRequestData(
			'/info/cpu/historical',
			CPUInfoHistoricalSchema,
			'GET',
			undefined,
			options
		);
	}
	return await apiRequestData('/info/cpu', CPUInfoSchema, 'GET', undefined, options);
}

export async function getCPUInfoResult(
	options?: NodeAPIDataRequestOptions
): Promise<CPUInfo | APIResponse> {
	return await apiRequestResult('/info/cpu', CPUInfoSchema, 'GET', undefined, options);
}

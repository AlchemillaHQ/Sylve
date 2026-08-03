import {
	CPUInfoHistoricalSchema,
	CPUInfoSchema,
	type CPUInfo,
	type CPUInfoHistorical
} from '$lib/types/info/cpu';
import { apiRequest } from '$lib/utils/http';
import type { NodeAPIRequestOptions } from '$lib/utils/http';

export async function getCPUInfo(
	queryType: 'current',
	options?: NodeAPIRequestOptions
): Promise<CPUInfo>;
export async function getCPUInfo(
	queryType: 'historical',
	options?: NodeAPIRequestOptions
): Promise<CPUInfoHistorical>;
export async function getCPUInfo(
	queryType?: 'current' | 'historical',
	options?: NodeAPIRequestOptions
): Promise<CPUInfo | CPUInfoHistorical> {
	if (queryType === 'historical') {
		return await apiRequest(
			'/info/cpu/historical',
			CPUInfoHistoricalSchema,
			'GET',
			undefined,
			options
		);
	}
	return await apiRequest('/info/cpu', CPUInfoSchema, 'GET', undefined, options);
}

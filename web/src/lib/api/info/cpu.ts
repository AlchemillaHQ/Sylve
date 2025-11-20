import {
	CPUInfoHistoricalSchema,
	CPUInfoSchema,
	type CPUInfo,
	type CPUInfoHistorical
} from '$lib/types/info/cpu';
import { apiRequest } from '$lib/utils/http';
import type { QueryFunctionContext } from '@tanstack/svelte-query';

export async function getCPUInfo(
	queryObj?: QueryFunctionContext
): Promise<CPUInfo | CPUInfoHistorical> {
	if (queryObj) {
		if (queryObj.queryKey.includes('cpuInfoHistorical')) {
			return await apiRequest('/info/cpu/historical', CPUInfoHistoricalSchema, 'GET');
		}
	}

	return await apiRequest('/info/cpu', CPUInfoSchema, 'GET');
}

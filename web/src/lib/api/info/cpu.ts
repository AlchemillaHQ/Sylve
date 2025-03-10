import {
	CPUInfoHistoricalSchema,
	CPUInfoSchema,
	type CPUInfo,
	type CPUInfoHistorical
} from '$lib/types/info/cpu';
import { apiRequest } from '$lib/utils/http';
import type { QueryFunctionContext } from '@sveltestack/svelte-query';

export async function getCPUInfo(
	queryObj: QueryFunctionContext | undefined
): Promise<CPUInfo | CPUInfoHistorical> {
	if (queryObj) {
		if (queryObj.queryKey.includes('cpuInfoHistorical')) {
			const data = await apiRequest('/info/cpu/historical', CPUInfoHistoricalSchema, 'GET');
			return CPUInfoHistoricalSchema.parse(data);
		}
	}

	const data = await apiRequest('/info/cpu', CPUInfoSchema, 'GET');
	return CPUInfoSchema.parse(data);
}

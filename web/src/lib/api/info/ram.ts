import {
	RAMInfoHistoricalSchema,
	RAMInfoSchema,
	type RAMInfo,
	type RAMInfoHistorical
} from '$lib/types/info/ram';
import { apiRequest } from '$lib/utils/http';
import type { QueryFunctionContext } from '@tanstack/svelte-query';

export async function getRAMInfo(
	queryObj?: QueryFunctionContext
): Promise<RAMInfo | RAMInfoHistorical> {
	if (queryObj) {
		if (queryObj.queryKey.includes('ramInfoHistorical')) {
			return await apiRequest('/info/ram/historical', RAMInfoHistoricalSchema, 'GET');
		}
	}

	return await apiRequest('/info/ram', RAMInfoSchema, 'GET');
}

export async function getSwapInfo(
	queryObj?: QueryFunctionContext
): Promise<RAMInfo | RAMInfoHistorical> {
	if (queryObj) {
		if (queryObj.queryKey.includes('swapInfoHistorical')) {
			return await apiRequest('/info/swap/historical', RAMInfoHistoricalSchema, 'GET');
		}
	}

	return await apiRequest('/info/swap', RAMInfoSchema, 'GET');
}

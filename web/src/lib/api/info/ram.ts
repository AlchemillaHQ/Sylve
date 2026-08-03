import {
	RAMInfoHistoricalSchema,
	RAMInfoSchema,
	type RAMInfo,
	type RAMInfoHistorical
} from '$lib/types/info/ram';
import { apiRequest } from '$lib/utils/http';
import type { NodeAPIRequestOptions } from '$lib/utils/http';

export async function getRAMInfo(
	queryType: 'current',
	options?: NodeAPIRequestOptions
): Promise<RAMInfo>;
export async function getRAMInfo(
	queryType: 'historical',
	options?: NodeAPIRequestOptions
): Promise<RAMInfoHistorical>;
export async function getRAMInfo(
	queryType?: 'current' | 'historical',
	options?: NodeAPIRequestOptions
): Promise<RAMInfo | RAMInfoHistorical> {
	if (queryType === 'historical') {
		return await apiRequest(
			'/info/ram/historical',
			RAMInfoHistoricalSchema,
			'GET',
			undefined,
			options
		);
	}
	return await apiRequest('/info/ram', RAMInfoSchema, 'GET', undefined, options);
}

export async function getSwapInfo(queryType: 'current'): Promise<RAMInfo>;
export async function getSwapInfo(queryType: 'historical'): Promise<RAMInfoHistorical>;
export async function getSwapInfo(
	queryType?: 'current' | 'historical'
): Promise<RAMInfo | RAMInfoHistorical> {
	if (queryType === 'historical') {
		return await apiRequest('/info/swap/historical', RAMInfoHistoricalSchema, 'GET');
	}
	return await apiRequest('/info/swap', RAMInfoSchema, 'GET');
}

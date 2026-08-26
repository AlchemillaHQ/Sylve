import {
	RAMInfoHistoricalSchema,
	RAMInfoSchema,
	type RAMInfo,
	type RAMInfoHistorical
} from '$lib/types/info/ram';
import type { APIResponse } from '$lib/types/common';
import { apiRequestData, apiRequestResult, type NodeAPIDataRequestOptions } from '$lib/utils/http';

export async function getRAMInfo(): Promise<RAMInfo>;
export async function getRAMInfo(
	queryType: 'current',
	options?: NodeAPIDataRequestOptions
): Promise<RAMInfo>;
export async function getRAMInfo(
	queryType: 'historical',
	options?: NodeAPIDataRequestOptions
): Promise<RAMInfoHistorical>;
export async function getRAMInfo(
	queryType?: 'current' | 'historical',
	options?: NodeAPIDataRequestOptions
): Promise<RAMInfo | RAMInfoHistorical> {
	if (queryType === 'historical') {
		return await apiRequestData(
			'/info/ram/historical',
			RAMInfoHistoricalSchema,
			'GET',
			undefined,
			options
		);
	}
	return await apiRequestData('/info/ram', RAMInfoSchema, 'GET', undefined, options);
}

export async function getRAMInfoResult(
	options?: NodeAPIDataRequestOptions
): Promise<RAMInfo | APIResponse> {
	return await apiRequestResult('/info/ram', RAMInfoSchema, 'GET', undefined, options);
}

export async function getSwapInfo(queryType: 'current'): Promise<RAMInfo>;
export async function getSwapInfo(queryType: 'historical'): Promise<RAMInfoHistorical>;
export async function getSwapInfo(
	queryType?: 'current' | 'historical'
): Promise<RAMInfo | RAMInfoHistorical> {
	if (queryType === 'historical') {
		return await apiRequestData('/info/swap/historical', RAMInfoHistoricalSchema, 'GET');
	}
	return await apiRequestData('/info/swap', RAMInfoSchema, 'GET');
}

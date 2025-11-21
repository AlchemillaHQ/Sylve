import {
	RAMInfoHistoricalSchema,
	RAMInfoSchema,
	type RAMInfo,
	type RAMInfoHistorical
} from '$lib/types/info/ram';
import { apiRequest } from '$lib/utils/http';

export async function getRAMInfo(queryType: 'current'): Promise<RAMInfo>;
export async function getRAMInfo(queryType: 'historical'): Promise<RAMInfoHistorical>;
export async function getRAMInfo(
	queryType?: 'current' | 'historical'
): Promise<RAMInfo | RAMInfoHistorical> {
	if (queryType === 'historical') {
		return await apiRequest('/info/ram/historical', RAMInfoHistoricalSchema, 'GET');
	}
	return await apiRequest('/info/ram', RAMInfoSchema, 'GET');
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

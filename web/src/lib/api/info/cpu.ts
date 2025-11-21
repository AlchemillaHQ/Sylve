import {
	CPUInfoHistoricalSchema,
	CPUInfoSchema,
	type CPUInfo,
	type CPUInfoHistorical
} from '$lib/types/info/cpu';
import { apiRequest } from '$lib/utils/http';

export async function getCPUInfo(queryType: 'current'): Promise<CPUInfo>;
export async function getCPUInfo(queryType: 'historical'): Promise<CPUInfoHistorical>;
export async function getCPUInfo(
	queryType?: 'current' | 'historical'
): Promise<CPUInfo | CPUInfoHistorical> {
	if (queryType === 'historical') {
		return await apiRequest('/info/cpu/historical', CPUInfoHistoricalSchema, 'GET');
	}
	return await apiRequest('/info/cpu', CPUInfoSchema, 'GET');
}

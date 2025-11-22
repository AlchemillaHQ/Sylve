import { getBasicInfo } from '$lib/api/info/basic';
import { getCPUInfo } from '$lib/api/info/cpu';
import { getNetworkInterfaceInfoHistorical } from '$lib/api/info/network';
import { getRAMInfo, getSwapInfo } from '$lib/api/info/ram';
import { getPoolsDiskUsage } from '$lib/api/zfs/pool';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;
	const [
		basicInfo,
		cpuInfo,
		cpuInfoHistorical,
		ramInfo,
		ramInfoHistorical,
		swapInfo,
		swapInfoHistorical,
		totalDiskUsage,
		networkUsageHistorical
	] = await Promise.all([
		cachedFetch('basicInfo', async () => getBasicInfo(), cacheDuration),
		cachedFetch('cpuInfo', async () => getCPUInfo('current'), cacheDuration),
		cachedFetch('cpuInfoHistorical', () => getCPUInfo('historical'), cacheDuration),
		cachedFetch('ramInfo', async () => getRAMInfo('current'), cacheDuration),
		cachedFetch('ramInfoHistorical', () => getRAMInfo('historical'), cacheDuration),
		cachedFetch('swapInfo', async () => getSwapInfo('current'), cacheDuration),
		cachedFetch('swapInfoHistorical', () => getSwapInfo('historical'), cacheDuration),
		cachedFetch('totalDiskUsage', async () => getPoolsDiskUsage(), cacheDuration),
		cachedFetch(
			'networkUsageHistorical',
			async () => getNetworkInterfaceInfoHistorical(),
			cacheDuration
		)
	]);

	return {
		basicInfo,
		cpuInfo,
		cpuInfoHistorical,
		ramInfo,
		ramInfoHistorical,
		swapInfo,
		swapInfoHistorical,
		totalDiskUsage,
		networkUsageHistorical
	};
}

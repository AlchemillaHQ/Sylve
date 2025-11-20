import { getBasicInfo } from '$lib/api/info/basic';
import { getCPUInfo } from '$lib/api/info/cpu';
import { getNetworkInterfaceInfoHistorical } from '$lib/api/info/network';
import { getRAMInfo, getSwapInfo } from '$lib/api/info/ram';
import { getIODelay, getPoolsDiskUsage } from '$lib/api/zfs/pool';
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
		cachedFetch('basicInfo', getBasicInfo, cacheDuration),
		cachedFetch('cpuInfo', getCPUInfo, cacheDuration),
		cachedFetch(
			'cpuInfoHistorical',
			() =>
				getCPUInfo({
					queryKey: ['cpuInfoHistorical'],
					meta: undefined
				}),
			cacheDuration
		),
		cachedFetch('ramInfo', getRAMInfo, cacheDuration),
		cachedFetch(
			'ramInfoHistorical',
			() => getRAMInfo({ queryKey: ['ramInfoHistorical'], meta: undefined }),
			cacheDuration
		),
		cachedFetch('swapInfo', getSwapInfo, cacheDuration),
		cachedFetch(
			'swapInfoHistorical',
			() => getSwapInfo({ queryKey: ['swapInfoHistorical'], meta: undefined }),
			cacheDuration
		),
		cachedFetch('totalDiskUsage', getPoolsDiskUsage, cacheDuration),
		cachedFetch('networkUsageHistorical', getNetworkInterfaceInfoHistorical, cacheDuration)
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

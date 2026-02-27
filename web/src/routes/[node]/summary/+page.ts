import { getBasicInfo } from '$lib/api/info/basic';
import { getCPUInfo } from '$lib/api/info/cpu';
import { getNetworkInterfaceInfoHistorical } from '$lib/api/info/network';
import { getRAMInfo, getSwapInfo } from '$lib/api/info/ram';
import { getPoolsDiskUsage } from '$lib/api/zfs/pool';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
    const cacheDuration = SEVEN_DAYS;
    const hostname = params.node;

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
        cachedFetch('basic-info', async () => getBasicInfo(), cacheDuration),
        cachedFetch('cpu-info', async () => getCPUInfo('current'), cacheDuration),
        cachedFetch('cpu-info-historical', () => getCPUInfo('historical'), cacheDuration),
        cachedFetch('ram-info', async () => getRAMInfo('current'), cacheDuration),
        cachedFetch('ram-info-historical', () => getRAMInfo('historical'), cacheDuration),
        cachedFetch('swap-info', async () => getSwapInfo('current'), cacheDuration),
        cachedFetch('swap-info-historical', () => getSwapInfo('historical'), cacheDuration),
        cachedFetch('total-disk-usage', async () => getPoolsDiskUsage(), cacheDuration),
        cachedFetch(
            'network-usage-historical',
            async () => getNetworkInterfaceInfoHistorical(),
            cacheDuration
        )
    ]);

    return {
        hostname,
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

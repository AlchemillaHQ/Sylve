import { getBasicInfo } from '$lib/api/info/basic';
import { getCPUInfo } from '$lib/api/info/cpu';
import { getRAMInfo, getSwapInfo } from '$lib/api/info/ram';
import { getNodeSummaryHistory } from '$lib/api/info/summary';
import { getPoolsDiskUsage } from '$lib/api/zfs/pool';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const hostname = params.node;

	const [basicInfo, cpuInfo, ramInfo, swapInfo, totalDiskUsage, summaryHistory] = await Promise.all(
		[
			cachedFetch('basic-info', async () => getBasicInfo(), cacheDuration),
			cachedFetch('cpu-info', async () => getCPUInfo('current'), cacheDuration),
			cachedFetch('ram-info', async () => getRAMInfo('current'), cacheDuration),
			cachedFetch('swap-info', async () => getSwapInfo('current'), cacheDuration),
			cachedFetch('total-disk-usage', async () => getPoolsDiskUsage(), cacheDuration),
			cachedFetch('node-summary-history', () => getNodeSummaryHistory(), cacheDuration)
		]
	);

	return {
		hostname,
		basicInfo,
		cpuInfo,
		ramInfo,
		swapInfo,
		totalDiskUsage,
		summaryHistory
	};
}

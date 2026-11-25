import { getSimpleContainers } from '$lib/api/container/container';
import { getBasicInfo } from '$lib/api/info/basic';
import { getCPUInfo } from '$lib/api/info/cpu';
import { getRAMInfo, getSwapInfo } from '$lib/api/info/ram';
import { getNodeSummaryHistory } from '$lib/api/info/summary';
import { getSimpleJails } from '$lib/api/jail/jail';
import { getInterfaces } from '$lib/api/network/iface';
import { getNotificationsCount } from '$lib/api/notifications';
import { getSimpleVMs } from '$lib/api/vm/vm';
import { getPoolsDiskUsageFull, getZFSDashboardSnapshot } from '$lib/api/zfs/pool';
import { ui } from '$lib/stores/interface.svelte';
import { CachedNodeSummaryHistorySchema } from '$lib/types/info/summary';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const hostname = params.node;
	const optional = <T>(key: string, fetch: () => Promise<T>) =>
		ui.zen ? cachedFetch(key, fetch, cacheDuration, false, hostname).catch(() => null) : null;

	const [
		basicInfo,
		cpuInfo,
		ramInfo,
		swapInfo,
		totalDiskUsage,
		summaryHistory,
		snapshot,
		interfaces,
		vms,
		jails,
		containers,
		alertCount
	] = await Promise.all([
		cachedFetch('basic-info', async () => getBasicInfo(), cacheDuration),
		cachedFetch('cpu-info', async () => getCPUInfo('current'), cacheDuration),
		cachedFetch('ram-info', async () => getRAMInfo('current'), cacheDuration),
		cachedFetch('swap-info', async () => getSwapInfo('current'), cacheDuration),
		cachedFetch('total-disk-usage-full', async () => getPoolsDiskUsageFull(), cacheDuration),
		ui.zen
			? cachedFetch(
					'node-summary-history',
					async () => ({
						...(await getNodeSummaryHistory(undefined, { hostname })),
						reconciledAt: Date.now()
					}),
					30_000,
					false,
					hostname,
					CachedNodeSummaryHistorySchema
				)
			: cachedFetch('node-summary-history', () => getNodeSummaryHistory(), cacheDuration),
		optional(`zfs-dashboard-snapshot:${hostname}`, () => getZFSDashboardSnapshot({ hostname })),
		optional('network-interfaces', () => getInterfaces({ hostname })),
		optional('simple-vms', () => getSimpleVMs(hostname)),
		optional('simple-jails', () => getSimpleJails(hostname)),
		optional('simple-containers', () => getSimpleContainers(hostname)),
		ui.zen
			? getNotificationsCount()
					.then((counts) => counts.active)
					.catch(() => null)
			: null
	]);

	return {
		hostname,
		basicInfo,
		cpuInfo,
		ramInfo,
		swapInfo,
		totalDiskUsage,
		summaryHistory,
		snapshot,
		interfaces,
		vms,
		jails,
		containers,
		alertCount
	};
}

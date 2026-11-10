import { getStatsBootstrap } from '$lib/api/container/container';
import { getCPUInfo } from '$lib/api/info/cpu.js';
import { getRAMInfo } from '$lib/api/info/ram.js';
import { hostDetail } from '$lib/stores/permissions.svelte';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
	const node = String(params.node);
	const ocId = Number(params.ocid);
	const [stats, ramInfo, cpuInfo] = await Promise.all([
		getStatsBootstrap(ocId, { hostname: node }),
		hostDetail(
			node,
			() =>
				cachedFetch(
					'system-ram-info',
					async () => getRAMInfo('current', { hostname: node }),
					SEVEN_DAYS,
					false,
					node
				),
			null
		),
		hostDetail(
			node,
			() =>
				cachedFetch(
					'system-cpu-info',
					async () => getCPUInfo('current', { hostname: node }),
					SEVEN_DAYS,
					false,
					node
				),
			null
		)
	]);

	return { stats: isAPIResponse(stats) ? null : stats, ramInfo, cpuInfo };
}

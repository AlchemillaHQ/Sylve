import { getCPUInfo } from '$lib/api/info/cpu.js';
import { getRAMInfo } from '$lib/api/info/ram.js';
import { getJailByCTID, getStatsBootstrap } from '$lib/api/jail/jail';
import { getNodes } from '$lib/api/cluster/cluster';
import type { ClusterNode } from '$lib/types/cluster/cluster';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	const node = String(params.node || '').trim();
	const ctId = Number(params.ctid);
	const cacheDuration = SEVEN_DAYS;
	if (!Number.isSafeInteger(ctId) || ctId <= 0) error(404, 'Invalid jail CTID');
	if (!node) error(400, 'Invalid node hostname');

	const [jailResult, statsResult, ramInfo, cpuInfo, nodes] = await Promise.all([
		cachedFetch(
			`jail-${ctId}`,
			async () => getJailByCTID(ctId, { hostname: node }),
			cacheDuration,
			false,
			node
		),
		cachedFetch(
			`guest-stats-v2:jail:${ctId}:bootstrap`,
			async () => getStatsBootstrap(ctId, { hostname: node }),
			cacheDuration,
			false,
			node
		),
		cachedFetch(
			'system-ram-info',
			async () => getRAMInfo('current', { hostname: node }),
			cacheDuration,
			false,
			node
		),
		cachedFetch(
			'system-cpu-info',
			async () => getCPUInfo('current', { hostname: node }),
			cacheDuration,
			false,
			node
		),
		cachedFetch('cluster-nodes', async () => getNodes(), 1000)
	]);
	if (isAPIResponse(jailResult)) {
		error(
			jailResult.message === 'jail_not_found' ? 404 : 502,
			jailResult.message || 'Unable to load jail'
		);
	}

	return {
		node,
		ctId,
		jail: jailResult,
		stats: isAPIResponse(statsResult) ? null : statsResult,
		ramInfo: ramInfo,
		cpuInfo: cpuInfo,
		nodes: nodes as ClusterNode[] | null
	};
}

import { getNodes } from '$lib/api/cluster/cluster';
import { getQGAInfo, getStatsBootstrap, getVmByIdResult } from '$lib/api/vm/vm';
import type { ClusterNode } from '$lib/types/cluster/cluster';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const node = String(params.node || '').trim();
	const rid = Number(params.rid);
	if (!Number.isSafeInteger(rid) || rid <= 0) error(400, 'Invalid VM RID');
	if (!node) error(400, 'Invalid node hostname');

	const [vm, statsResult, gaInfoCached, nodes] = await Promise.all([
		cachedFetch(
			`vm-${rid}`,
			async () => {
				const result = await getVmByIdResult(rid, { hostname: node });
				if (isAPIResponse(result)) {
					error(
						result.message === 'vm_not_found' ? 404 : 502,
						result.message || 'Unable to load VM'
					);
				}
				return result;
			},
			cacheDuration,
			false,
			node
		),
		cachedFetch(
			`guest-stats-v2:vm:${rid}:bootstrap`,
			async () => getStatsBootstrap(rid, { hostname: node }),
			cacheDuration,
			false,
			node
		),
		cachedFetch(
			`vm-qga-${rid}`,
			async () => getQGAInfo(rid, { hostname: node }),
			cacheDuration,
			true,
			node
		),
		cachedFetch('cluster-nodes', async () => getNodes(), 1000)
	]);

	return {
		node,
		rid,
		vm,
		stats: isAPIResponse(statsResult) ? null : statsResult,
		gaInfo: vm?.qemuGuestAgent === true ? gaInfoCached : null,
		nodes: nodes as ClusterNode[] | null
	};
}

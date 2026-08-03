import { getNodes } from '$lib/api/cluster/cluster';
import { getQGAInfo, getStatsBootstrap, getVmById } from '$lib/api/vm/vm';
import type { ClusterNode } from '$lib/types/cluster/cluster';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
    const cacheDuration = SEVEN_DAYS;
    const node = String(params.node);
    const rid = Number(params.rid);

    const [vm, statsResult, gaInfoCached, nodes] = await Promise.all([
        cachedFetch(
            `vm-${rid}`,
            async () => getVmById(rid, 'rid', { hostname: node }),
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
        node: node,
        rid: rid,
        vm: vm,
        stats: isAPIResponse(statsResult) ? null : statsResult,
        gaInfo: vm?.qemuGuestAgent === true ? gaInfoCached : null,
        nodes: nodes as ClusterNode[] | null
    };
}

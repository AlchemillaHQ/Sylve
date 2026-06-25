import { getNodes } from '$lib/api/cluster/cluster';
import { getQGAInfo, getStats, getVmById } from '$lib/api/vm/vm';
import type { ClusterNode } from '$lib/types/cluster/cluster';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
    const cacheDuration = SEVEN_DAYS;
    const node = String(params.node);
    const rid = Number(params.rid);

    const [vm, stats, gaInfoCached, nodes] = await Promise.all([
        cachedFetch(`vm-${rid}`, async () => getVmById(rid, 'rid'), cacheDuration),
        cachedFetch(`vm-stats-${rid}`, async () => getStats(rid, 'hourly'), cacheDuration),
        cachedFetch(`vm-qga-${rid}`, async () => getQGAInfo(rid), cacheDuration, true),
        cachedFetch('cluster-nodes', async () => getNodes(), 1000)
    ]);

    return {
        node: node,
        rid: rid,
        vm: vm,
        stats: stats,
        gaInfo: vm?.qemuGuestAgent === true ? gaInfoCached : null,
        nodes: nodes as ClusterNode[] | null
    };
}

import { getCPUInfo } from '$lib/api/info/cpu.js';
import { getRAMInfo } from '$lib/api/info/ram.js';
import { getJailById, getStatsBootstrap } from '$lib/api/jail/jail';
import { getNodes } from '$lib/api/cluster/cluster';
import type { ClusterNode } from '$lib/types/cluster/cluster';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
    const node = params.node;
    const ctId = Number(params.ctid);
    const cacheDuration = SEVEN_DAYS;

    const [jail, statsResult, ramInfo, cpuInfo, nodes] = await Promise.all([
        cachedFetch(
            `jail-${ctId}`,
            async () => getJailById(ctId, 'ctid', { hostname: node }),
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

    return {
        node,
        ctId,
        jail: jail,
        stats: isAPIResponse(statsResult) ? null : statsResult,
        ramInfo: ramInfo,
        cpuInfo: cpuInfo,
        nodes: nodes as ClusterNode[] | null
    };
}

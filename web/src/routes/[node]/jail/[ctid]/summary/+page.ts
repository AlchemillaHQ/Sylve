import { getCPUInfo } from '$lib/api/info/cpu.js';
import { getRAMInfo } from '$lib/api/info/ram.js';
import { getJailById, getStats } from '$lib/api/jail/jail';
import { getNodes } from '$lib/api/cluster/cluster';
import type { ClusterNode } from '$lib/types/cluster/cluster';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
    const node = params.node;
    const ctId = Number(params.ctid);
    const cacheDuration = SEVEN_DAYS;

    const [jail, stats, ramInfo, cpuInfo, nodes] = await Promise.all([
        cachedFetch(`jail-${ctId}`, async () => getJailById(ctId, 'ctid'), cacheDuration),
        cachedFetch(`jail-${ctId}-stats`, async () => getStats(ctId, 'hourly'), cacheDuration),
        cachedFetch('system-ram-info', async () => getRAMInfo('current'), cacheDuration),
        cachedFetch('system-cpu-info', async () => getCPUInfo('current'), cacheDuration),
        cachedFetch('cluster-nodes', async () => getNodes(), 1000)
    ]);

    return {
        node,
        ctId,
        jail: jail,
        stats: stats,
        ramInfo: ramInfo,
        cpuInfo: cpuInfo,
        nodes: nodes as ClusterNode[] | null
    };
}

import { listBackupJobs, listBackupTargets } from '$lib/api/cluster/backups';
import { getNodes } from '$lib/api/cluster/cluster';
import type { BackupJob, BackupTarget } from '$lib/types/cluster/backups';
import type { ClusterNode } from '$lib/types/cluster/cluster';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
    const [targets, nodes, jobs] = await Promise.all([
        cachedFetch('backup-targets', async () => listBackupTargets(), 1000),
        cachedFetch('cluster-nodes', async () => getNodes(), 1000),
        cachedFetch('backup-jobs', async () => listBackupJobs(), 1000)
    ]);

    return {
        targets: targets as BackupTarget[],
        nodes: nodes as ClusterNode[],
        jobs: jobs as BackupJob[]
    };
}

import { listBackupTargets } from '$lib/api/cluster/backups';
import type { BackupTarget } from '$lib/types/cluster/backups';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
    const targets = await cachedFetch('backup-targets', async () => listBackupTargets(), 1000);

    return {
        targets: targets as BackupTarget[]
    };
}

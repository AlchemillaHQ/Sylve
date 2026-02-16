import { listBackupJobs, listBackupTargets } from '$lib/api/cluster/backups';
import type { BackupJob, BackupTarget } from '$lib/types/cluster/backups';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const [targets, jobs] = await Promise.all([
		cachedFetch('backup-targets', async () => listBackupTargets(), 1000),
		cachedFetch('backup-jobs', async () => listBackupJobs(), 1000)
	]);

	return {
		targets: targets as BackupTarget[],
		jobs: jobs as BackupJob[]
	};
}

import { listBackupJobsResult, listBackupTargetsResult } from '$lib/api/cluster/backups';
import { getDetails, getNodesResult } from '$lib/api/cluster/cluster';
import { getBasicInfo } from '$lib/api/info/basic';
import type { BackupGuestFilter } from '$lib/types/cluster/backups';
import {
	isPositiveSafeInteger,
	resolveBackupJobsPageData,
	type BackupJobsPageData
} from '$lib/utils/backup-jobs-page-state';
import { cachedFetch } from '$lib/utils/http';

export function backupJobsCacheKey(guest?: BackupGuestFilter): string {
	return guest ? `backup-jobs-${guest.kind}-${guest.id}` : 'backup-jobs';
}

export async function loadBackupJobsPageData(
	guest?: BackupGuestFilter
): Promise<BackupJobsPageData> {
	if (guest && !isPositiveSafeInteger(guest.id)) {
		throw new Error('invalid_guest_id');
	}

	const jobsCacheKey = backupJobsCacheKey(guest);
	const [targets, jobs, nodes, details, basicInfo] = await Promise.all([
		cachedFetch('backup-targets', async () => listBackupTargetsResult(), 1000),
		cachedFetch(jobsCacheKey, async () => listBackupJobsResult(undefined, guest), 1000),
		cachedFetch('cluster-nodes', async () => getNodesResult(), 1000),
		cachedFetch('cluster-details', async () => getDetails(), 1000),
		cachedFetch('basic-info', async () => getBasicInfo(), 1000)
	]);

	return resolveBackupJobsPageData({ targets, jobs, nodes, details, basicInfo });
}

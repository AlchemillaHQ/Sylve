import { listBackupJobsResult, listBackupTargetsResult } from '$lib/api/cluster/backups';
import { getDetails, getNodesResult } from '$lib/api/cluster/cluster';
import { getBasicInfo } from '$lib/api/info/basic';
import {
	isPositiveSafeInteger,
	resolveBackupJobsPageData,
	type BackupJobsPageData
} from '$lib/utils/backup-jobs-page-state';
import { cachedFetch } from '$lib/utils/http';

export async function loadBackupJobsPageData(vmRid?: number): Promise<BackupJobsPageData> {
	const scopedToVM = vmRid !== undefined;
	if (vmRid !== undefined && !isPositiveSafeInteger(vmRid)) {
		throw new Error('invalid_vm_rid');
	}

	const jobsCacheKey = scopedToVM ? `backup-jobs-vm-${vmRid}` : 'backup-jobs';
	const [targets, jobs, nodes, details, basicInfo] = await Promise.all([
		cachedFetch('backup-targets', async () => listBackupTargetsResult(), 1000),
		cachedFetch(jobsCacheKey, async () => listBackupJobsResult(undefined, vmRid), 1000),
		cachedFetch('cluster-nodes', async () => getNodesResult(), 1000),
		cachedFetch('cluster-details', async () => getDetails(), 1000),
		cachedFetch('basic-info', async () => getBasicInfo(), 1000)
	]);

	return resolveBackupJobsPageData({ targets, jobs, nodes, details, basicInfo });
}

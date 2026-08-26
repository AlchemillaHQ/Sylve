import { loadBackupJobsPageData } from '$lib/utils/backup-jobs-page';

export async function load() {
	return loadBackupJobsPageData();
}

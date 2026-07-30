import { loadBackupJobsPageData } from '$lib/utils/backup-jobs-page';
import { parsePositiveSafeInteger } from '$lib/utils/backup-jobs-page-state';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	const rid = parsePositiveSafeInteger(params.rid);
	if (rid === null) {
		error(404, 'Invalid VM ID');
	}

	const data = await loadBackupJobsPageData(rid);

	return {
		...data,
		rid,
		hostname: params.node
	};
}

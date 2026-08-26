import { loadBackupJobsPageData } from '$lib/utils/backup-jobs-page';
import { parsePositiveSafeInteger } from '$lib/utils/backup-jobs-page-state';
import type { BackupGuestScope } from '$lib/types/cluster/backups';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	const rid = parsePositiveSafeInteger(params.rid);
	if (rid === null) {
		error(404, 'Invalid VM ID');
	}

	const guestScope: BackupGuestScope = {
		kind: 'vm',
		id: rid,
		hostname: params.node
	};
	const data = await loadBackupJobsPageData(guestScope);

	return {
		...data,
		guestScope
	};
}

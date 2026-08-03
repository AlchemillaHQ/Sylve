import type { BackupGuestScope } from '$lib/types/cluster/backups';
import { loadBackupJobsPageData } from '$lib/utils/backup-jobs-page';
import { parsePositiveSafeInteger } from '$lib/utils/backup-jobs-page-state';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	const ctId = parsePositiveSafeInteger(params.ctid);
	if (ctId === null) {
		error(404, 'Invalid jail CTID');
	}

	const guestScope: BackupGuestScope = {
		kind: 'jail',
		id: ctId,
		hostname: params.node
	};
	const data = await loadBackupJobsPageData(guestScope);

	return {
		...data,
		guestScope
	};
}

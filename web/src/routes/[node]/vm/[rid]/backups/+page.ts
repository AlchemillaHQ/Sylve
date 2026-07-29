import { loadBackupJobsPageData } from '$lib/utils/backup-jobs-page';

export async function load({ params }) {
	const rid = Number(params.rid);
	const data = await loadBackupJobsPageData(rid);

	return {
		...data,
		rid,
		hostname: params.node
	};
}

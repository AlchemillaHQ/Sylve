import { listGroups } from '$lib/api/auth/groups';
import { getSambaShares } from '$lib/api/samba/share';
import { getDatasets } from '$lib/api/zfs/datasets';
import { GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;
	const [datasets, shares, groups] = await Promise.all([
		cachedFetch(
			'zfs-filesystems',
			async () => await getDatasets(GZFSDatasetTypeSchema.enum.FILESYSTEM),
			cacheDuration
		),
		cachedFetch('samba-shares', async () => await getSambaShares(), cacheDuration),
		cachedFetch('groups', async () => await listGroups(), cacheDuration)
	]);

	return {
		datasets,
		shares,
		groups
	};
}

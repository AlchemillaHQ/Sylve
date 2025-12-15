import { listDisks } from '$lib/api/disk/disk';
import { getPools } from '$lib/api/zfs/pool';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;
	const [disks, pools] = await Promise.all([
		cachedFetch('disk-list', async () => await listDisks(), cacheDuration),
		cachedFetch('pool-list', async () => await getPools(false), cacheDuration)
	]);

	return {
		disks,
		pools
	};
}

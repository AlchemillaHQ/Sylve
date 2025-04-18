import { listDisks } from '$lib/api/disk/disk';
import { getPools } from '$lib/api/zfs/pool';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = 1000 * 60000;
	const [disks, pools] = await Promise.all([
		cachedFetch('disks', async () => await listDisks(), cacheDuration),
		cachedFetch('pools', getPools, cacheDuration)
	]);

	return {
		disks,
		pools
	};
}

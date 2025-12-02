import { getDatasets } from '$lib/api/zfs/datasets';
import { getPools } from '$lib/api/zfs/pool';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;
	const [datasets, pools] = await Promise.all([
		cachedFetch('zfs-filesystems', async () => await getDatasets('filesystem'), cacheDuration),
		cachedFetch('pools', () => getPools(false), cacheDuration)
	]);

	return {
		pools: pools,
		datasets: datasets
	};
}

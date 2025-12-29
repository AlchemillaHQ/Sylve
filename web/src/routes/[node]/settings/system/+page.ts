import { getPools } from '$lib/api/zfs/pool';
import { getBasicSettings } from '$lib/api/system/settings';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;
	const [pools, basicSettings] = await Promise.all([
		cachedFetch('zfs-pools-full', async () => await getPools(true), cacheDuration),
		cachedFetch('system-basic-settings', async () => await getBasicSettings(), cacheDuration)
	]);

	return {
		pools,
		basicSettings
	};
}

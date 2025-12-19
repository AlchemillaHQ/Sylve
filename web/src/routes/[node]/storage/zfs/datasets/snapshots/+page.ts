import { getBasicSettings } from '$lib/api/system/settings';
import { getPeriodicSnapshots } from '$lib/api/zfs/datasets';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;
	const [basicSettings, periodicSnapshots] = await Promise.all([
		cachedFetch('basic-settings', () => getBasicSettings(), cacheDuration),
		cachedFetch('zfs-periodic-snapshots', async () => await getPeriodicSnapshots(), cacheDuration)
	]);

	return {
		basicSettings: basicSettings,
		periodicSnapshots: periodicSnapshots
	};
}

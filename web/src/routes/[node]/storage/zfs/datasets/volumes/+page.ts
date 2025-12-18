import { getBasicSettings } from '$lib/api/system/settings';
import { getDownloads } from '$lib/api/utilities/downloader';
import { getDatasets } from '$lib/api/zfs/datasets';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;

	const [datasets, downloads, settings] = await Promise.all([
		cachedFetch('zfs-volumes', async () => await getDatasets('volume'), cacheDuration),
		cachedFetch('downloads', async () => getDownloads(), cacheDuration),
		cachedFetch('basic-settings', async () => getBasicSettings(), cacheDuration)
	]);

	return {
		datasets: datasets,
		downloads: downloads,
		settings: settings
	};
}

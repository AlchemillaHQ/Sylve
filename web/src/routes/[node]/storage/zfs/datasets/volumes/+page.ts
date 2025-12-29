import { getBasicSettings } from '$lib/api/system/settings';
import { getDownloads } from '$lib/api/utilities/downloader';
import { getDatasets } from '$lib/api/zfs/datasets';
import { GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;

	const [datasets, downloads, settings] = await Promise.all([
		cachedFetch(
			'zfs-volumes',
			async () => await getDatasets(GZFSDatasetTypeSchema.enum.VOLUME),
			cacheDuration
		),
		cachedFetch('downloads', async () => getDownloads(), cacheDuration),
		cachedFetch('basic-settings', async () => getBasicSettings(), cacheDuration)
	]);

	return {
		datasets: datasets,
		downloads: downloads,
		settings: settings
	};
}

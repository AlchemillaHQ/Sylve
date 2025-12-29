import { getBasicSettings } from '$lib/api/system/settings';
import { getDatasets } from '$lib/api/zfs/datasets';
import { GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;
	const [settings, datasets] = await Promise.all([
		cachedFetch('basic-settings', () => getBasicSettings(), cacheDuration),
		cachedFetch(
			'zfs-filesystems',
			async () => await getDatasets(GZFSDatasetTypeSchema.enum.FILESYSTEM),
			cacheDuration
		)
	]);

	return {
		settings: settings,
		datasets: datasets
	};
}

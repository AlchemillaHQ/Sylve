import { basicSettingsOrFallback, getBasicSettings } from '$lib/api/system/settings';
import { getDownloadsResult } from '$lib/api/utilities/downloader';
import { getDatasets } from '$lib/api/zfs/datasets';
import { GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const node = params.node;

	const [datasets, downloadsResult, settings] = await Promise.all([
		cachedFetch(
			'zfs-volumes',
			async () => await getDatasets(GZFSDatasetTypeSchema.enum.VOLUME),
			cacheDuration
		),
		cachedFetch(
			'download-list',
			async () => getDownloadsResult({ hostname: node }),
			cacheDuration,
			false,
			node
		),
		cachedFetch('basic-settings', async () => getBasicSettings(), cacheDuration)
	]);

	return {
		node,
		datasets: datasets,
		downloads: isAPIResponse(downloadsResult) ? [] : downloadsResult,
		settings: basicSettingsOrFallback(settings),
		loadErrors: isAPIResponse(downloadsResult) ? [downloadsResult] : []
	};
}

import { basicSettingsOrFallback, getBasicSettings } from '$lib/api/system/settings';
import { getDownloadsResult } from '$lib/api/utilities/downloader';
import { getDatasets, getDetailedDatasetsResult } from '$lib/api/zfs/datasets';
import { ui } from '$lib/stores/interface.svelte';
import { canReadHost } from '$lib/stores/permissions.svelte';
import { DetailedDatasetSchema, GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	const hostname = params.node;
	if (ui.zen) {
		if (!(await canReadHost(hostname))) {
			return {
				hostname,
				node: hostname,
				denied: true,
				datasets: [],
				downloads: [],
				settings: { pools: [], services: [], initialized: false },
				loadErrors: [],
				fresh: false
			};
		}
		let fresh = false;
		const datasets = await cachedFetch(
			'zfs-volumes-detailed',
			() => {
				fresh = true;
				return getDetailedDatasetsResult(GZFSDatasetTypeSchema.enum.VOLUME, hostname);
			},
			SEVEN_DAYS,
			false,
			hostname,
			DetailedDatasetSchema.array()
		);
		const pools = Array.isArray(datasets)
			? [...new Set(datasets.map((dataset) => dataset.pool))]
			: [];
		return {
			hostname,
			node: hostname,
			denied: false,
			datasets,
			downloads: [],
			settings: { pools, services: [], initialized: true },
			loadErrors: [],
			fresh
		};
	}

	const [datasets, downloadsResult, settings] = await Promise.all([
		cachedFetch(
			'zfs-volumes',
			async () => await getDatasets(GZFSDatasetTypeSchema.enum.VOLUME),
			SEVEN_DAYS
		),
		cachedFetch(
			'download-list',
			async () => getDownloadsResult({ hostname }),
			SEVEN_DAYS,
			false,
			hostname
		),
		cachedFetch('basic-settings', async () => getBasicSettings(), SEVEN_DAYS)
	]);

	return {
		hostname,
		node: hostname,
		denied: false,
		datasets,
		downloads: isAPIResponse(downloadsResult) ? [] : downloadsResult,
		settings: basicSettingsOrFallback(settings),
		loadErrors: isAPIResponse(downloadsResult) ? [downloadsResult] : [],
		fresh: true
	};
};

import { basicSettingsOrFallback, getBasicSettings } from '$lib/api/system/settings';
import { getDatasets, getDetailedDatasetsResult } from '$lib/api/zfs/datasets';
import { ui } from '$lib/stores/interface.svelte';
import { canReadHost } from '$lib/stores/permissions.svelte';
import { DetailedDatasetSchema, GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	const hostname = params.node;
	if (ui.zen) {
		if (!(await canReadHost(hostname))) {
			return {
				hostname,
				denied: true,
				datasets: [],
				settings: { pools: [], services: [], initialized: false },
				fresh: false
			};
		}
		let fresh = false;
		const datasets = await cachedFetch(
			'zfs-filesystems',
			() => {
				fresh = true;
				return getDetailedDatasetsResult(GZFSDatasetTypeSchema.enum.FILESYSTEM, hostname);
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
			denied: false,
			datasets,
			settings: { pools, services: [], initialized: true },
			fresh
		};
	}

	const [settings, datasets] = await Promise.all([
		cachedFetch('basic-settings', () => getBasicSettings(), SEVEN_DAYS),
		cachedFetch(
			'zfs-filesystems',
			async () => await getDatasets(GZFSDatasetTypeSchema.enum.FILESYSTEM),
			SEVEN_DAYS
		)
	]);

	return {
		hostname,
		denied: false,
		datasets,
		settings: basicSettingsOrFallback(settings),
		fresh: true
	};
};

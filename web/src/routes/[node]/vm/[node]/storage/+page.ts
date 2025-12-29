import { getDownloads } from '$lib/api/utilities/downloader';
import { getVMDomain, getVMs } from '$lib/api/vm/vm';
import { getDatasets } from '$lib/api/zfs/datasets';
import { getPools } from '$lib/api/zfs/pool';
import { GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const rid = params.node;

	const [vms, domain, filesystems, volumes, pools, downloads] = await Promise.all([
		cachedFetch('vm-list', async () => getVMs(), cacheDuration),
		cachedFetch(`vm-domain-${rid}`, async () => getVMDomain(Number(rid)), cacheDuration),
		cachedFetch('zfs-datasets', async () => await getDatasets(), cacheDuration),
		cachedFetch(
			'zfs-volumes',
			async () => await getDatasets(GZFSDatasetTypeSchema.enum.VOLUME),
			cacheDuration
		),
		cachedFetch('pools', async () => getPools(), cacheDuration),
		cachedFetch('downloads', async () => getDownloads(), cacheDuration)
	]);

	return {
		vms: vms,
		rid: rid,
		domain: domain,
		filesystems: filesystems,
		volumes: volumes,
		pools: pools,
		downloads: downloads
	};
}

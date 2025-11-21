import { getDownloads } from '$lib/api/utilities/downloader';
import { getVMDomain, getVMs } from '$lib/api/vm/vm';
import { getDatasets } from '$lib/api/zfs/datasets';
import { getPools } from '$lib/api/zfs/pool';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const vmId = params.node;

	const [vms, domain, datasets, pools, downloads] = await Promise.all([
		cachedFetch('vm-list', async () => getVMs(), cacheDuration),
		cachedFetch(`vm-domain-${vmId}`, async () => getVMDomain(Number(vmId)), cacheDuration),
		cachedFetch('datasets', async () => await getDatasets(), cacheDuration),
		cachedFetch('pools', async () => getPools(), cacheDuration),
		cachedFetch('downloads', async () => getDownloads(), cacheDuration)
	]);

	return {
		vms: vms,
		vmId: vmId,
		domain: domain,
		datasets: datasets,
		pools: pools,
		downloads: downloads
	};
}

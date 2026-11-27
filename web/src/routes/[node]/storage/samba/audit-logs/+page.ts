import { getSambaShares } from '$lib/api/samba/share';
import { getDatasets } from '$lib/api/zfs/datasets';
import { ui } from '$lib/stores/interface.svelte';
import { canReadHost } from '$lib/stores/permissions.svelte';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	const hostname = params.node;
	if (ui.zen) {
		return { zen: true as const, hostname, denied: !(await canReadHost(hostname)) };
	}

	const cacheDuration = SEVEN_DAYS;
	const [datasets, shares] = await Promise.all([
		cachedFetch('datasets', async () => await getDatasets(), cacheDuration),
		cachedFetch('samba-shares', async () => await getSambaShares(), cacheDuration)
	]);

	return {
		zen: false as const,
		datasets,
		shares
	};
};

import { listDisks, listDisksResult } from '$lib/api/disk/disk';
import { ui } from '$lib/stores/interface.svelte';
import { canReadHost } from '$lib/stores/permissions.svelte';
import { DiskSchema } from '$lib/types/disk/disk';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	const hostname = params.node;
	if (ui.zen) {
		if (!(await canReadHost(hostname))) return { hostname, denied: true, disks: [], fresh: false };
		let fresh = false;
		const disks = await cachedFetch(
			'disk-list-inventory',
			() => {
				fresh = true;
				return listDisksResult('none', { hostname });
			},
			SEVEN_DAYS,
			false,
			hostname,
			DiskSchema.array()
		);
		return { hostname, denied: false, disks, fresh };
	}

	const disks = await cachedFetch('disk-list', () => listDisks(), SEVEN_DAYS);
	return { hostname, denied: false, disks, fresh: true };
};

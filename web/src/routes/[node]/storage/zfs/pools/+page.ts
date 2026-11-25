/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { listDisks, listDisksResult } from '$lib/api/disk/disk';
import { getPools, getPoolsResult } from '$lib/api/zfs/pool';
import { ui } from '$lib/stores/interface.svelte';
import { canReadHost } from '$lib/stores/permissions.svelte';
import { DiskSchema } from '$lib/types/disk/disk';
import { ZpoolSchema } from '$lib/types/zfs/pool';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	const hostname = params.node;
	if (ui.zen) {
		if (!(await canReadHost(hostname))) return { hostname, denied: true, pools: [], disks: [] };
		const fresh = { pools: false, disks: false };
		const [pools, disks] = await Promise.all([
			cachedFetch(
				'pool-list',
				() => {
					fresh.pools = true;
					return getPoolsResult(false, { hostname });
				},
				SEVEN_DAYS,
				false,
				hostname,
				ZpoolSchema.array()
			),
			cachedFetch(
				'disk-list-inventory',
				() => {
					fresh.disks = true;
					return listDisksResult('none', { hostname });
				},
				SEVEN_DAYS,
				false,
				hostname,
				DiskSchema.array()
			)
		]);
		return { hostname, denied: false, pools, disks, fresh };
	}

	const cacheDuration = SEVEN_DAYS;
	const [disks, pools] = await Promise.all([
		cachedFetch('disk-list-inventory', async () => await listDisks('none'), cacheDuration),
		cachedFetch('pool-list', async () => await getPools(false), cacheDuration)
	]);

	return {
		hostname,
		denied: false,
		disks,
		pools
	};
};

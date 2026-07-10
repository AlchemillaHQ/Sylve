/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { listDisks } from '$lib/api/disk/disk';
import { getPools } from '$lib/api/zfs/pool';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;
	const [disks, pools] = await Promise.all([
		cachedFetch('disk-list-inventory', async () => await listDisks('none'), cacheDuration),
		cachedFetch('pool-list', async () => await getPools(false), cacheDuration)
	]);

	return {
		disks,
		pools
	};
}

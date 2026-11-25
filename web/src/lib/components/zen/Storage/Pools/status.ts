/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { storage } from '$lib';
import { getPoolStatusResult } from '$lib/api/zfs/pool';
import { canReadHost, permissions } from '$lib/stores/permissions.svelte';
import { ZPoolStatusPoolSchema, type Zpool, type ZpoolStatusPool } from '$lib/types/zfs/pool';
import {
	APIRequestError,
	cachedFetch,
	isAPIResponse,
	isRequestCancellation,
	updateCache
} from '$lib/utils/http';

export async function loadPoolStatus(
	hostname: string,
	guid: string,
	options: { signal?: AbortSignal; fresh?: boolean } = {}
) {
	const token = storage.token;
	const permitted = await canReadHost(hostname);
	const grants = permissions.data;
	function checkCurrent() {
		options.signal?.throwIfAborted();
		if (token !== storage.token || grants !== permissions.data)
			throw new DOMException('Pool status access changed.', 'AbortError');
	}
	checkCurrent();
	if (!permitted) throw new Error('Pool status requires host read access.');

	const key = `zen-pool-status-${guid}`;
	if (!options.fresh && grants) {
		const cached = await cachedFetch(
			key,
			async () => null,
			30_000,
			true,
			hostname,
			ZPoolStatusPoolSchema.refine((status) => status.pool_guid === guid).nullable()
		);
		checkCurrent();
		if (cached) return cached;
	}

	const result = await getPoolStatusResult(guid, { hostname, signal: options.signal });
	checkCurrent();
	if (isAPIResponse(result)) throw new APIRequestError(result);
	if (result.pool_guid !== guid)
		throw new Error('Pool status identity did not match the selected pool.');
	await updateCache(key, result, hostname);
	checkCurrent();
	return result;
}

export async function preloadPoolStatuses(
	hostname: string,
	pools: Zpool[],
	options: {
		signal?: AbortSignal;
		fresh?: boolean;
		onStatus?: (status: ZpoolStatusPool) => void;
	} = {}
) {
	const entries = await Promise.all(
		pools.map(async (pool) => {
			try {
				const status = await loadPoolStatus(hostname, pool.guid, options);
				options.signal?.throwIfAborted();
				options.onStatus?.(status);
				return [pool.guid, status] as const;
			} catch (error) {
				if (isRequestCancellation(error)) throw error;
				return null;
			}
		})
	);
	return Object.fromEntries(entries.filter((entry) => entry !== null));
}

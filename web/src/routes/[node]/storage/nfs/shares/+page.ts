import { getNFSDatasets, getNFSShares, getNFSSharesResult, getNFSStatusResult } from '$lib/api/nfs';
import { ui } from '$lib/stores/interface.svelte';
import { canReadHost } from '$lib/stores/permissions.svelte';
import { NFSDatasetSchema, NFSShareSchema, NFSStatusSchema } from '$lib/types/nfs';
import { ONLY_CACHE, SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';
import type { PageLoad } from './$types';

export const load = (async ({ params }) => {
	const node = params.node;
	if (ui.zen) {
		if (!(await canReadHost(node))) {
			return {
				zen: true as const,
				hostname: node,
				denied: true,
				shares: [],
				fresh: false,
				status: null,
				statusFresh: false
			};
		}
		let fresh = false;
		let statusFresh = false;
		const [shares, status] = await Promise.all([
			cachedFetch(
				'nfs-shares',
				() => {
					fresh = true;
					return getNFSSharesResult({ hostname: node });
				},
				SEVEN_DAYS,
				false,
				node,
				NFSShareSchema.array()
			),
			cachedFetch(
				'nfs-status',
				() => {
					statusFresh = true;
					return getNFSStatusResult({ hostname: node });
				},
				SEVEN_DAYS,
				false,
				node,
				NFSStatusSchema
			)
		]);
		return {
			zen: true as const,
			hostname: node,
			denied: false,
			shares,
			fresh,
			status,
			statusFresh
		};
	}

	const [shares, datasets] = await Promise.allSettled([
		cachedFetch(
			'nfs-shares',
			() => getNFSShares({ hostname: node }),
			SEVEN_DAYS,
			ONLY_CACHE,
			node,
			NFSShareSchema.array()
		),
		cachedFetch(
			'nfs-datasets',
			() => getNFSDatasets({ hostname: node }),
			SEVEN_DAYS,
			ONLY_CACHE,
			node,
			NFSDatasetSchema.array()
		)
	]);
	return {
		zen: false as const,
		node,
		shares: shares.status === 'fulfilled' ? (shares.value ?? []) : [],
		datasets: datasets.status === 'fulfilled' ? (datasets.value ?? []) : []
	};
}) satisfies PageLoad;

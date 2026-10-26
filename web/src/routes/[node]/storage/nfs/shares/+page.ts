import { getNFSDatasets, getNFSShares } from '$lib/api/nfs';
import { NFSDatasetSchema, NFSShareSchema } from '$lib/types/nfs';
import { ONLY_CACHE, SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';
import type { PageLoad } from './$types';

export const load = (async ({ params }) => {
	const node = params.node;
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
		node,
		shares: shares.status === 'fulfilled' ? (shares.value ?? []) : [],
		datasets: datasets.status === 'fulfilled' ? (datasets.value ?? []) : []
	};
}) satisfies PageLoad;

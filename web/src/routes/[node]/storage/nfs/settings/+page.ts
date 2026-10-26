import { getNFSConfig } from '$lib/api/nfs';
import { NFSConfigSchema } from '$lib/types/nfs';
import { ONLY_CACHE, SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';
import type { PageLoad } from './$types';

export const load = (async ({ params }) => {
	try {
		const config = await cachedFetch(
			'nfs-config',
			() => getNFSConfig({ hostname: params.node }),
			SEVEN_DAYS,
			ONLY_CACHE,
			params.node,
			NFSConfigSchema
		);
		return { node: params.node, config };
	} catch {
		return { node: params.node, config: null };
	}
}) satisfies PageLoad;

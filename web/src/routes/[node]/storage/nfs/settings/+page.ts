import { getNFSConfig, getNFSConfigResult, getNFSStatusResult } from '$lib/api/nfs';
import { ui } from '$lib/stores/interface.svelte';
import { canReadHost } from '$lib/stores/permissions.svelte';
import { NFSConfigSchema, NFSStatusSchema } from '$lib/types/nfs';
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
				config: null,
				fresh: false,
				status: null,
				statusFresh: false
			};
		}
		let fresh = false;
		let statusFresh = false;
		const [config, status] = await Promise.all([
			cachedFetch(
				'nfs-config',
				() => {
					fresh = true;
					return getNFSConfigResult({ hostname: node });
				},
				SEVEN_DAYS,
				false,
				node,
				NFSConfigSchema
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
			config,
			fresh,
			status,
			statusFresh
		};
	}

	try {
		const config = await cachedFetch(
			'nfs-config',
			() => getNFSConfig({ hostname: node }),
			SEVEN_DAYS,
			ONLY_CACHE,
			node,
			NFSConfigSchema
		);
		return { zen: false as const, node, config };
	} catch {
		return { zen: false as const, node, config: null };
	}
}) satisfies PageLoad;

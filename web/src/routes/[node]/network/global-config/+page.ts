import { getNetworkGlobalConfig } from '$lib/api/network/global-config';

export async function load({ params }) {
	try {
		return {
			node: params.node,
			config: await getNetworkGlobalConfig({ hostname: params.node }),
			loadFailed: false
		};
	} catch {
		return { node: params.node, config: null, loadFailed: true };
	}
}

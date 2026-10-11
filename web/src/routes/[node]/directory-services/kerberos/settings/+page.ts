import { getKerberosSettings } from '$lib/api/directory-services';
export async function load({ params }) {
	try {
		return {
			node: params.node,
			settings: await getKerberosSettings({ hostname: params.node }),
			loadFailed: false
		};
	} catch {
		return { node: params.node, settings: null, loadFailed: true };
	}
}

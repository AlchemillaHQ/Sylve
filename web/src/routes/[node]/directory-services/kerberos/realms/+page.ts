import { getKerberosRealms } from '$lib/api/directory-services';
export async function load({ params }) {
	try {
		return {
			node: params.node,
			realms: await getKerberosRealms({ hostname: params.node }),
			loadFailed: false
		};
	} catch {
		return { node: params.node, realms: [], loadFailed: true };
	}
}

import { getKerberosKeytabs } from '$lib/api/directory-services';
export async function load({ params }) {
	try {
		return {
			node: params.node,
			keytabs: await getKerberosKeytabs({ hostname: params.node }),
			loadFailed: false
		};
	} catch {
		return { node: params.node, keytabs: [], loadFailed: true };
	}
}

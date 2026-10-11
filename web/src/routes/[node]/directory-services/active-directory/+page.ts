import {
	getActiveDirectory,
	getADStatus,
	getKerberosRealms,
	getKerberosKeytabs
} from '$lib/api/directory-services';

export async function load({ params }) {
	try {
		const [ad, status, realms, keytabs] = await Promise.all([
			getActiveDirectory({ hostname: params.node }),
			getADStatus({ hostname: params.node }),
			getKerberosRealms({ hostname: params.node }),
			getKerberosKeytabs({ hostname: params.node })
		]);
		return { node: params.node, ad, status, realms, keytabs, loadFailed: false };
	} catch {
		return { node: params.node, ad: null, status: null, realms: [], keytabs: [], loadFailed: true };
	}
}

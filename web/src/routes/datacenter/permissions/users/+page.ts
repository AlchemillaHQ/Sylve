import { storage } from '$lib';
import { listUsers } from '$lib/api/auth/local';
import { listRealms } from '$lib/api/auth/permissions';
import type { APIResponse } from '$lib/types/common';
import { isAPIResponse } from '$lib/utils/http';

export async function load() {
	// User IDs are per node, and so are accounts that aren't shared (admin, name clashes).
	const node = storage.hostname ?? '';
	const [local, realm, realms] = await Promise.all([
		listUsers('local', { hostname: node }),
		listUsers('oidc', { hostname: node }),
		listRealms()
	]);
	const loadErrors: APIResponse[] = [local, realm].filter(isAPIResponse);
	const realmLabels: Record<string, string> = isAPIResponse(realms)
		? {}
		: Object.fromEntries(realms.map((r) => [r.name, r.label || r.name]));
	return {
		node,
		users: [...(isAPIResponse(local) ? [] : local), ...(isAPIResponse(realm) ? [] : realm)],
		realmLabels,
		loadErrors
	};
}

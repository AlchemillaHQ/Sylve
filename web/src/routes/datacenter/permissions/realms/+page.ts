import { listACLGroups, listRealms } from '$lib/api/auth/permissions';
import type { APIResponse } from '$lib/types/common';
import { isAPIResponse } from '$lib/utils/http';

export async function load() {
	const [realms, groups] = await Promise.all([listRealms(), listACLGroups()]);
	const loadErrors: APIResponse[] = [realms, groups].filter(isAPIResponse);
	return {
		realms: isAPIResponse(realms) ? [] : realms,
		groups: isAPIResponse(groups) ? [] : groups,
		loadErrors
	};
}

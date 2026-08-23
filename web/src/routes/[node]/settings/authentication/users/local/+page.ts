import { listGroups } from '$lib/api/auth/groups';
import { listUsers } from '$lib/api/auth/local';
import type { APIResponse } from '$lib/types/common';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
	const node = params.node;
	const [usersResult, groupsResult] = await Promise.all([
		cachedFetch(
			'users_local',
			async () => await listUsers('local', { hostname: node }),
			SEVEN_DAYS,
			false,
			node
		),
		cachedFetch('groups', async () => await listGroups({ hostname: node }), SEVEN_DAYS, false, node)
	]);

	const loadErrors: APIResponse[] = [];
	if (isAPIResponse(usersResult)) loadErrors.push(usersResult);
	if (isAPIResponse(groupsResult)) loadErrors.push(groupsResult);

	return {
		node,
		users: isAPIResponse(usersResult) ? [] : usersResult,
		groups: isAPIResponse(groupsResult) ? [] : groupsResult,
		loadErrors
	};
}

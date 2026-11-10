import { listUsers } from '$lib/api/auth/local';
import { listACLGroups } from '$lib/api/auth/permissions';
import type { APIResponse } from '$lib/types/common';
import { isAPIResponse } from '$lib/utils/http';

export async function load() {
	const [groups, users] = await Promise.all([listACLGroups(), listUsers()]);
	const loadErrors: APIResponse[] = [groups, users].filter(isAPIResponse);
	return {
		groups: isAPIResponse(groups) ? [] : groups,
		users: isAPIResponse(users) ? [] : users,
		loadErrors
	};
}

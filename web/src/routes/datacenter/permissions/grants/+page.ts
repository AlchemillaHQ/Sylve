import { listUsers } from '$lib/api/auth/local';
import { listACLGroups, listGrants, listRoles } from '$lib/api/auth/permissions';
import type { APIResponse } from '$lib/types/common';
import { isAPIResponse } from '$lib/utils/http';

export async function load() {
	const [grants, roles, users, groups] = await Promise.all([
		listGrants(),
		listRoles(),
		listUsers(),
		listACLGroups()
	]);

	const loadErrors: APIResponse[] = [grants, roles, users, groups].filter(isAPIResponse);

	return {
		grants: isAPIResponse(grants) ? [] : grants,
		roles: isAPIResponse(roles) ? [] : roles,
		users: isAPIResponse(users) ? [] : users,
		groups: isAPIResponse(groups) ? [] : groups,
		loadErrors
	};
}

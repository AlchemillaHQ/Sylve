import { listRoles } from '$lib/api/auth/permissions';
import { isAPIResponse } from '$lib/utils/http';

export async function load() {
	const roles = await listRoles();
	return {
		roles: isAPIResponse(roles) ? [] : roles,
		loadErrors: isAPIResponse(roles) ? [roles] : []
	};
}

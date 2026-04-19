import { getNotificationTransports } from '$lib/api/notifications';
import { listUsers } from '$lib/api/auth/local';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load() {
	const [response, usersResponse] = await Promise.all([
		cachedFetch('notification-config', async () => await getNotificationTransports(), SEVEN_DAYS),
		cachedFetch('users', async () => await listUsers(), SEVEN_DAYS)
	]);

	const config = isAPIResponse(response)
		? {
				transports: []
			}
		: response;
	const users = Array.isArray(usersResponse) ? usersResponse : [];

	return {
		config,
		users
	};
}

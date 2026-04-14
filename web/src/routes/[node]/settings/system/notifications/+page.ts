import { getNotificationTransports } from '$lib/api/notifications';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load() {
	const response = await cachedFetch(
		'notification-config',
		async () => await getNotificationTransports(),
		SEVEN_DAYS
	);

	const config = isAPIResponse(response)
		? {
				transports: []
			}
		: response;

	return {
		config
	};
}

import { getNotificationConfig } from '$lib/api/notifications';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load() {
	const response = await cachedFetch(
		'notification-config',
		async () => await getNotificationConfig(),
		SEVEN_DAYS
	);

	const config = isAPIResponse(response)
		? {
				ntfy: {
					enabled: false,
					baseUrl: 'https://ntfy.sh',
					topic: '',
					hasAuthToken: false
				},
				email: {
					enabled: false,
					smtpHost: '',
					smtpPort: 587,
					smtpUsername: '',
					smtpFrom: '',
					smtpUseTls: true,
					recipients: [],
					hasPassword: false
				}
			}
		: response;

	return {
		config
	};
}

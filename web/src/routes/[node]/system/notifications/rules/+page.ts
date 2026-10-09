import { getNotificationRules } from '$lib/api/notifications';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load() {
	const response = await cachedFetch(
		'notification-rules',
		async () => await getNotificationRules(),
		SEVEN_DAYS
	);

	const rules = isAPIResponse(response)
		? {
				rules: [],
				templates: []
			}
		: response;

	return {
		rules
	};
}

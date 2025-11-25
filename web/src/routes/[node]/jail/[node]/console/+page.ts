import { getJails, getJailStates } from '$lib/api/jail/jail';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const [jails, jailStates] = await Promise.all([
		cachedFetch('jail-list', async () => getJails(), cacheDuration),
		cachedFetch('jail-states', async () => getJailStates(), cacheDuration)
	]);

	return {
		jails: jails,
		jailStates: jailStates
	};
}

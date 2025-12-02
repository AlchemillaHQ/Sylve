import { getJails, getJailStates } from '$lib/api/jail/jail';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const ctId = parseInt(params.node);

	const [jail, state] = await Promise.all([
		cachedFetch(`jail-${ctId}`, async () => getJails(), cacheDuration),
		cachedFetch(`jail-${ctId}-state`, async () => getJailStates(), cacheDuration)
	]);

	return {
		ctId,
		jail,
		state
	};
}

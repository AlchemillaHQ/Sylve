import { getJailById, getJailStateById } from '$lib/api/jail/jail';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const ctId = parseInt(params.node, 10);

	const [jail, state] = await Promise.all([
		cachedFetch(`jail-${ctId}`, async () => getJailById(ctId, 'ctid'), cacheDuration),
		cachedFetch(`jail-${ctId}-state`, async () => getJailStateById(ctId), cacheDuration)
	]);

	return {
		ctId: ctId,
		jail: jail,
		state: state
	};
}

import { getSimpleJailById, getJailStateById } from '$lib/api/jail/jail';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export const ssr = false;
export const prerender = false;
export const csr = true;

export async function load({ params }) {
	const ctId = Number(params.ctid);

	const [jail, state] = await Promise.all([
		cachedFetch(`simple-jail-${ctId}`, async () => getSimpleJailById(ctId, 'ctid'), SEVEN_DAYS),
		cachedFetch(`jail-${ctId}-state`, async () => getJailStateById(ctId), SEVEN_DAYS)
	]);

	return {
		ctId,
		jail,
		state
	};
}

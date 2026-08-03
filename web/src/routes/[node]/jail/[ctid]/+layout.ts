import { getSimpleJailById, getJailStateById } from '$lib/api/jail/jail';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export const ssr = false;
export const prerender = false;
export const csr = true;

export async function load({ params }) {
	const ctId = Number(params.ctid);
	const node = String(params.node);

	const [jail, state] = await Promise.all([
		cachedFetch(
			`simple-jail-${ctId}`,
			async () => getSimpleJailById(ctId, 'ctid', { hostname: node }),
			SEVEN_DAYS,
			false,
			node
		),
		cachedFetch(
			`jail-${ctId}-state`,
			async () => getJailStateById(ctId, { hostname: node }),
			SEVEN_DAYS,
			false,
			node
		)
	]);

	return {
		node,
		ctId,
		jail,
		state
	};
}

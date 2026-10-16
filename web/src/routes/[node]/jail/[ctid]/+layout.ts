import { getJailState, getSimpleJailByCTID } from '$lib/api/jail/jail';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import { error } from '@sveltejs/kit';

export const ssr = false;
export const prerender = false;
export const csr = true;

export async function load({ params }) {
	const ctId = Number(params.ctid);
	const node = String(params.node || '').trim();
	if (!Number.isSafeInteger(ctId) || ctId <= 0) error(404, 'Invalid jail CTID');
	if (!node) error(400, 'Invalid node hostname');

	const [cachedJailResult, stateResult] = await Promise.all([
		cachedFetch(
			`simple-jail-${ctId}`,
			async () => getSimpleJailByCTID(ctId, { hostname: node }),
			SEVEN_DAYS,
			false,
			node
		),
		cachedFetch(
			`jail-${ctId}-state`,
			async () => getJailState(ctId, { hostname: node }),
			SEVEN_DAYS,
			false,
			node
		)
	]);
	let jailResult = cachedJailResult;
	if (!isAPIResponse(jailResult) && !jailResult.type) {
		jailResult = await getSimpleJailByCTID(ctId, { hostname: node });
	}
	if (isAPIResponse(jailResult)) {
		error(
			jailResult.message === 'jail_not_found' ? 404 : 502,
			jailResult.message || 'Unable to load jail'
		);
	}
	if (isAPIResponse(stateResult)) {
		error(
			stateResult.message === 'jail_not_found' ? 404 : 502,
			stateResult.message || 'Unable to load jail state'
		);
	}

	return {
		node,
		ctId,
		jail: jailResult,
		state: stateResult
	};
}

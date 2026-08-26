import { storage } from '$lib';
import { getSimpleJailByCTID } from '$lib/api/jail/jail';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import { sha256 } from '$lib/utils/string';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const ctId = Number(params.ctid);
	const node = String(params.node || '').trim();
	if (!Number.isSafeInteger(ctId) || ctId <= 0) error(404, 'Invalid jail CTID');
	if (!node) error(400, 'Invalid node hostname');

	const jailResult = await cachedFetch(
		`simple-jail-${ctId}`,
		async () => getSimpleJailByCTID(ctId, { hostname: node }),
		cacheDuration,
		false,
		node
	);
	if (isAPIResponse(jailResult)) {
		error(
			jailResult.message === 'jail_not_found' ? 404 : 502,
			jailResult.message || 'Unable to load jail'
		);
	}
	const hash = await sha256(storage.token || '', 1);

	return {
		node,
		ctId,
		jail: jailResult,
		hash
	};
}

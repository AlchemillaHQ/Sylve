import { listJailSnapshots } from '$lib/api/jail/snapshots';
import type { APIResponse } from '$lib/types/common';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const ctId = Number(params.ctid);
	const node = String(params.node);
	const cacheKey = `jail-${ctId}-snapshots`;

	const result = await cachedFetch(
		cacheKey,
		async () => listJailSnapshots(ctId, { hostname: node, preserveErrors: true }),
		cacheDuration,
		false,
		node
	);

	return {
		node,
		ctId,
		snapshots: isAPIResponse(result) ? [] : result,
		snapshotsError: (isAPIResponse(result) ? result : null) as APIResponse | null
	};
}

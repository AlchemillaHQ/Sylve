import { listVMSnapshots } from '$lib/api/vm/snapshots';
import type { APIResponse } from '$lib/types/common';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const rid = Number(params.rid);
	const node = params.node;
	const cacheKey = 'vm-' + rid + '-snapshots';

	const result = await cachedFetch(
		cacheKey,
		async () => listVMSnapshots(rid, { hostname: node, preserveErrors: true }),
		cacheDuration,
		false,
		node
	);

	return {
		rid,
		node,
		snapshots: isAPIResponse(result) ? [] : result,
		snapshotsError: (isAPIResponse(result) ? result : null) as APIResponse | null
	};
}

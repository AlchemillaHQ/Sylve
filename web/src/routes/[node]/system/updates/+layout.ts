import { getHostUpdates } from '$lib/api/system/updates';
import { isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
	const node = String(params.node);
	const result = await getHostUpdates({ hostname: node });
	return {
		node,
		updates: isAPIResponse(result) ? null : result,
		updatesError: isAPIResponse(result) ? result : null
	};
}

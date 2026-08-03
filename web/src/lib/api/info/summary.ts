import {
	NodeSummaryHistorySchema,
	type NodeSummaryHistory,
	type SummaryHistoryCursors
} from '$lib/types/info/summary';
import { apiRequest, isAPIResponse, type NodeAPIRequestOptions } from '$lib/utils/http';

export async function getNodeSummaryHistory(
	cursors?: SummaryHistoryCursors,
	options?: NodeAPIRequestOptions
): Promise<NodeSummaryHistory> {
	let endpoint = '/info/summary/history';
	if (cursors) {
		const query = new URLSearchParams({
			cpuAfter: String(cursors.cpu),
			ramAfter: String(cursors.ram),
			networkAfter: String(cursors.network)
		});
		endpoint = `/info/summary/history/delta?${query.toString()}`;
	}

	const response = await apiRequest(endpoint, NodeSummaryHistorySchema, 'GET', undefined, options);
	if (isAPIResponse(response)) {
		const message = Array.isArray(response.error)
			? response.error.join(', ')
			: response.error || response.message || 'Unable to load node summary history.';
		throw new Error(message);
	}
	return response;
}

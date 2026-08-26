import {
	NodeSummaryHistorySchema,
	type NodeSummaryHistory,
	type SummaryHistoryCursors
} from '$lib/types/info/summary';
import { apiRequestData, type NodeAPIDataRequestOptions } from '$lib/utils/http';

export async function getNodeSummaryHistory(
	cursors?: SummaryHistoryCursors,
	options?: NodeAPIDataRequestOptions
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

	return await apiRequestData(endpoint, NodeSummaryHistorySchema, 'GET', undefined, options);
}

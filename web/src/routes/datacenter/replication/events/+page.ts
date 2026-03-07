import { getNodes } from '$lib/api/cluster/cluster';
import {
	listReplicationEvents,
	listReplicationPolicies,
	listReplicationReceipts
} from '$lib/api/cluster/replication';
import type { ClusterNode } from '$lib/types/cluster/cluster';
import type {
	ReplicationEvent,
	ReplicationPolicy,
	ReplicationReceipt
} from '$lib/types/cluster/replication';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const [policies, events, receipts, nodes] = await Promise.all([
		cachedFetch('replication-policies', async () => listReplicationPolicies(), 1000),
		cachedFetch('replication-events', async () => listReplicationEvents(200), 1000),
		cachedFetch('replication-receipts', async () => listReplicationReceipts(), 1000),
		cachedFetch('cluster-nodes', async () => getNodes(), 1000)
	]);

	return {
		policies: policies as ReplicationPolicy[],
		events: events as ReplicationEvent[],
		receipts: receipts as ReplicationReceipt[],
		nodes: nodes as ClusterNode[]
	};
}

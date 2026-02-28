import { listReplicationEvents, listReplicationPolicies } from '$lib/api/cluster/replication';
import type { ReplicationEvent, ReplicationPolicy } from '$lib/types/cluster/replication';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const [policies, events] = await Promise.all([
		cachedFetch('replication-policies', async () => listReplicationPolicies(), 1000),
		cachedFetch('replication-events', async () => listReplicationEvents(200), 1000)
	]);

	return {
		policies: policies as ReplicationPolicy[],
		events: events as ReplicationEvent[]
	};
}

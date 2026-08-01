import { listReplicationPolicies } from '$lib/api/cluster/replication';
import { getNodes } from '$lib/api/cluster/cluster';
import type { ClusterNode } from '$lib/types/cluster/cluster';
import type { ReplicationPolicy } from '$lib/types/cluster/replication';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const [policies, nodes] = await Promise.all([
		cachedFetch('replication-policies', async () => listReplicationPolicies(), 1000),
		cachedFetch('cluster-nodes', async () => getNodes(), 1000)
	]);

	return {
		policies: policies as ReplicationPolicy[],
		nodes: nodes as ClusterNode[]
	};
}

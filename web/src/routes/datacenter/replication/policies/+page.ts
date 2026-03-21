import { listReplicationPolicies, listReplicationReceipts } from '$lib/api/cluster/replication';
import { getNodes } from '$lib/api/cluster/cluster';
import { getSimpleJails } from '$lib/api/jail/jail';
import { getSimpleVMs } from '$lib/api/vm/vm';
import type { ClusterNode } from '$lib/types/cluster/cluster';
import type { ReplicationPolicy, ReplicationReceipt } from '$lib/types/cluster/replication';
import type { SimpleJail } from '$lib/types/jail/jail';
import type { SimpleVm } from '$lib/types/vm/vm';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const [policies, receipts, nodes, jails, vms] = await Promise.all([
		cachedFetch('replication-policies', async () => listReplicationPolicies(), 1000),
		cachedFetch('replication-receipts', async () => listReplicationReceipts(), 1000),
		cachedFetch('cluster-nodes', async () => getNodes(), 1000),
		cachedFetch('simple-jails', async () => getSimpleJails(), 1000),
		cachedFetch('simple-vms', async () => getSimpleVMs(), 1000)
	]);

	return {
		policies: policies as ReplicationPolicy[],
		receipts: receipts as ReplicationReceipt[],
		nodes: nodes as ClusterNode[],
		jails: jails as SimpleJail[],
		vms: vms as SimpleVm[]
	};
}

import { listBackupTargetsResult } from '$lib/api/cluster/backups';
import { getDetails, getNodesResult } from '$lib/api/cluster/cluster';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load() {
	const [targetsResult, clusterDetailsResult, clusterNodesResult] = await Promise.all([
		cachedFetch('backup-targets', async () => listBackupTargetsResult(), 1000),
		cachedFetch('cluster-details', async () => getDetails(), 1000),
		cachedFetch('cluster-nodes', async () => getNodesResult(), 1000)
	]);

	return {
		targets: isAPIResponse(targetsResult) ? [] : targetsResult,
		clusterDetails: isAPIResponse(clusterDetailsResult) ? null : clusterDetailsResult,
		clusterNodes: isAPIResponse(clusterNodesResult) ? [] : clusterNodesResult,
		availability: {
			targets: !isAPIResponse(targetsResult),
			cluster: !isAPIResponse(clusterDetailsResult),
			nodes: !isAPIResponse(clusterNodesResult)
		}
	};
}

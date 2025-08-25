import { getCluster } from '$lib/api/datacenter/cluster.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = 1000;

	const [cluster] = await Promise.all([
		cachedFetch('cluster-info', async () => getCluster(), cacheDuration)
	]);

	return {
		cluster
	};
}

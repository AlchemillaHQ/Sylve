import { getDetails } from '$lib/api/cluster/cluster.js';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
    const cacheDuration = SEVEN_DAYS;
    const cluster = await cachedFetch('cluster-info', async () => getDetails(), cacheDuration);

    return {
        cluster
    };
}

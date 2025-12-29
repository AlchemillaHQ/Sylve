import { getNetworkObjects } from '$lib/api/network/object';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const [objects] = await Promise.all([
		cachedFetch('network-objects', async () => await getNetworkObjects(), SEVEN_DAYS)
	]);

	return {
		objects
	};
}

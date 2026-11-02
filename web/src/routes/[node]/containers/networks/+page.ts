import { getContainerNetworks } from '$lib/api/container/resources';
import { getSwitches } from '$lib/api/network/switch';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const node = params.node;
	const [networks, switches] = await Promise.all([
		cachedFetch('container-networks', () => getContainerNetworks(node), SEVEN_DAYS, false, node),
		getSwitches(node)
	]);

	return { node, networks, switches };
}

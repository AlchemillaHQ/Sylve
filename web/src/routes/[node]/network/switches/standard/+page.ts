import { getInterfaces } from '$lib/api/network/iface';
import { getHostInterfaceL3 } from '$lib/api/network/ifaceL3';
import { getNetworkObjects } from '$lib/api/network/object';
import { getSwitches } from '$lib/api/network/switch';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = 1000 * 60000;
	const [interfaces, switches, objects, hostInterfaceL3] = await Promise.all([
		cachedFetch('network-interfaces', async () => await getInterfaces(), cacheDuration),
		cachedFetch('network-switches', async () => await getSwitches(), cacheDuration),
		cachedFetch('network-objects', async () => await getNetworkObjects(), cacheDuration),
		getHostInterfaceL3()
	]);

	return {
		interfaces,
		switches,
		objects,
		hostInterfaceL3
	};
}

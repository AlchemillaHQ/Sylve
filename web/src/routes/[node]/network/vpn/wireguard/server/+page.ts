import { getInterfaces } from '$lib/api/network/iface';
import { getSwitches } from '$lib/api/network/switch';
import { getWireGuardServer } from '$lib/api/network/wireguard';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const [server, interfaces, switches] = await Promise.all([
		cachedFetch('network-vpn-wireguard-server', async () => await getWireGuardServer(), SEVEN_DAYS),
		cachedFetch('network-interfaces', async () => await getInterfaces(), SEVEN_DAYS),
		cachedFetch('network-switches', async () => await getSwitches(), SEVEN_DAYS)
	]);

	return {
		server,
		interfaces,
		switches
	};
}

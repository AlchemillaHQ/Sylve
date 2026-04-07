import { getInterfaces } from '$lib/api/network/iface';
import { getWireGuardServer } from '$lib/api/network/wireguard';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const [server, interfaces] = await Promise.all([
		cachedFetch('network-vpn-wireguard-server', async () => await getWireGuardServer(), SEVEN_DAYS),
		cachedFetch('network-ifaces', async () => await getInterfaces(), SEVEN_DAYS)
	]);

	return {
		server,
		interfaces
	};
}

import { getWireGuardClients } from '$lib/api/network/wireguard';
import { cachedFetch } from '$lib/utils/http';

const WIREGUARD_RUNTIME_CACHE_TTL = 5_000;

export async function load() {
	const clients = await cachedFetch(
		'network-vpn-wireguard-clients',
		async () => await getWireGuardClients(),
		WIREGUARD_RUNTIME_CACHE_TTL
	);

	return {
		clients
	};
}

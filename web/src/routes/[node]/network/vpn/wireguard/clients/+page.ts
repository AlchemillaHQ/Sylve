import { getWireGuardClients } from '$lib/api/network/wireguard';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const clients = await cachedFetch(
		'network-vpn-wireguard-clients',
		async () => await getWireGuardClients(),
		SEVEN_DAYS
	);

	return {
		clients
	};
}

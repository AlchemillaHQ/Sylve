import { getDHCPConfig } from '$lib/api/network/dhcp';
import { getInterfaces } from '$lib/api/network/iface';
import { getSwitches } from '$lib/api/network/switch';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = 1000 * 60000;
	const [interfaces, switches, dhcpConfig] = await Promise.all([
		cachedFetch('network-interfaces', async () => await getInterfaces(), cacheDuration),
		cachedFetch('network-switches', async () => await getSwitches(), cacheDuration),
		cachedFetch('dhcp-config', async () => await getDHCPConfig(), cacheDuration)
	]);

	return {
		interfaces,
		switches,
		dhcpConfig
	};
}

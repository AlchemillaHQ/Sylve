import { getDHCPConfig, getDHCPRanges, getLeases } from '$lib/api/network/dhcp';
import { getSwitches } from '$lib/api/network/switch';
import { cachedFetch } from '$lib/utils/http';
import { getNetworkObjects } from '$lib/api/network/object';

export async function load() {
	const cacheDuration = 1000 * 60000;
	const [switches, dhcpConfig, dhcpRanges, dhcpLeases, networkObjects] = await Promise.all([
		cachedFetch('network-switches', async () => await getSwitches(), cacheDuration),
		cachedFetch('dhcp-config', async () => await getDHCPConfig(), cacheDuration),
		cachedFetch('dhcp-ranges', async () => await getDHCPRanges(), cacheDuration),
		cachedFetch('dhcp-leases', async () => await getLeases(), cacheDuration),
		cachedFetch('network-objects', async () => await getNetworkObjects(), cacheDuration)
	]);

	return {
		switches,
		dhcpConfig,
		dhcpRanges,
		dhcpLeases,
		networkObjects
	};
}

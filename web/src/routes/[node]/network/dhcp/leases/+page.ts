import { getDHCPRanges, getLeases } from '$lib/api/network/dhcp';
import { cachedFetch } from '$lib/utils/http';
import { getNetworkObjects } from '$lib/api/network/object';

export async function load() {
	const cacheDuration = 1000 * 60000;
	const [dhcpRanges, dhcpLeases, networkObjects] = await Promise.all([
		cachedFetch('dhcp-ranges', async () => await getDHCPRanges(), cacheDuration),
		cachedFetch('dhcp-leases', async () => await getLeases(), cacheDuration),
		cachedFetch('network-objects', async () => await getNetworkObjects(), cacheDuration)
	]);

	return {
		dhcpRanges,
		dhcpLeases,
		networkObjects
	};
}

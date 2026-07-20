import { getDynamicDNSEntries } from '$lib/api/services/dynamic-dns';
import { getInterfaces } from '$lib/api/network/iface';
import { getSwitches } from '$lib/api/network/switch';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const [entryResult, interfaceResult, switchResult] = await Promise.all([
		cachedFetch('dynamic-dns-entries', async () => await getDynamicDNSEntries(), SEVEN_DAYS),
		cachedFetch('network-interfaces', async () => await getInterfaces(), SEVEN_DAYS),
		cachedFetch('network-switches', async () => await getSwitches(), SEVEN_DAYS)
	]);

	return {
		entries: Array.isArray(entryResult) ? entryResult : [],
		interfaces: Array.isArray(interfaceResult) ? interfaceResult : [],
		switches: switchResult ?? {}
	};
}

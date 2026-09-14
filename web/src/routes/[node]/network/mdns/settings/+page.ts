import { getMdnsSettings } from '$lib/api/network/mdns';
import { getInterfaces } from '$lib/api/network/iface';
import { getSwitches } from '$lib/api/network/switch';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;
	const [settings, interfaces, switches] = await Promise.all([
		cachedFetch('mdns-settings', async () => await getMdnsSettings(), cacheDuration),
		cachedFetch('network-interfaces', async () => await getInterfaces(), cacheDuration),
		cachedFetch('network-switches', async () => await getSwitches(), cacheDuration)
	]);

	return {
		settings,
		interfaces,
		switches
	};
}

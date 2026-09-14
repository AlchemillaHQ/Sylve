import { getInterfaces } from '$lib/api/network/iface';
import { getSwitches } from '$lib/api/network/switch';
import { getSambaConfig } from '$lib/api/samba/config';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;
	const [interfaces, sambaConfig, switches] = await Promise.all([
		cachedFetch('network-interfaces', async () => await getInterfaces(), cacheDuration),
		cachedFetch('samba-config', async () => await getSambaConfig(), cacheDuration),
		cachedFetch('network-switches', async () => await getSwitches(), cacheDuration)
	]);

	return {
		interfaces,
		sambaConfig,
		switches
	};
}

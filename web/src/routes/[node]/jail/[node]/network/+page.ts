import { getJailById, getJailStateById } from '$lib/api/jail/jail';
import { getInterfaces } from '$lib/api/network/iface';
import { getNetworkObjects } from '$lib/api/network/object';
import { getSwitches } from '$lib/api/network/switch';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const ctId = parseInt(params.node, 10);

	const [jail, state, interfaces, switches, networkObjects] = await Promise.all([
		cachedFetch(`jail-${ctId}`, async () => getJailById(ctId, 'ctid'), cacheDuration),
		cachedFetch(`jail-${ctId}-state`, async () => getJailStateById(ctId), cacheDuration),
		cachedFetch('network-interfaces', async () => await getInterfaces(), cacheDuration),
		cachedFetch('network-switches', async () => await getSwitches(), cacheDuration),
		cachedFetch('network-objects', async () => await getNetworkObjects(), cacheDuration)
	]);

	return {
		ctId: ctId,
		jail: jail,
		state: state,
		interfaces,
		switches,
		networkObjects
	};
}

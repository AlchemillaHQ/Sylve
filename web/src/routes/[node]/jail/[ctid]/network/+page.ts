import { getJailById } from '$lib/api/jail/jail';
import { getNetworkObjects } from '$lib/api/network/object';
import { getSwitches } from '$lib/api/network/switch';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
    const cacheDuration = SEVEN_DAYS;
    const ctId = Number(params.ctid);

    const [jail, switches, networkObjects] = await Promise.all([
        cachedFetch(`jail-${ctId}`, async () => getJailById(ctId, 'ctid'), cacheDuration),
        cachedFetch('network-switches', async () => await getSwitches(), cacheDuration),
        cachedFetch('network-objects', async () => await getNetworkObjects(), cacheDuration)
    ]);

    return {
        ctId: ctId,
        jail: jail,
        switches: switches,
        networkObjects: networkObjects
    };
}

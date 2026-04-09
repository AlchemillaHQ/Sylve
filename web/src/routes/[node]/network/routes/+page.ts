import { getInterfaces } from '$lib/api/network/iface';
import { getNetworkObjects } from '$lib/api/network/object';
import { getStaticRoutes } from '$lib/api/network/route';
import { getSwitches } from '$lib/api/network/switch';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
    const [routes, interfaces, switches, objects] = await Promise.all([
        cachedFetch('network-static-routes', async () => await getStaticRoutes(), SEVEN_DAYS),
        cachedFetch('network-ifaces', async () => await getInterfaces(), SEVEN_DAYS),
        cachedFetch('network-switches', async () => await getSwitches(), SEVEN_DAYS),
        cachedFetch('network-objects', async () => await getNetworkObjects(), SEVEN_DAYS)
    ]);

    return {
        routes,
        interfaces,
        switches,
        objects
    };
}

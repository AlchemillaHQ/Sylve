import { getInterfaces } from '$lib/api/network/iface';
import { getNetworkObjects } from '$lib/api/network/object.js';
import { getSwitches } from '$lib/api/network/switch';
import { getVmById, getVMDomain } from '$lib/api/vm/vm';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
    const cacheDuration = 1000 * 60000;
    const rid = Number(params.rid);

    const [vm, domain, interfaces, switches, networkObjects] = await Promise.all([
        cachedFetch(`vm-${rid}`, async () => getVmById(rid, 'rid'), cacheDuration),
        cachedFetch(`vm-domain-${rid}`, async () => getVMDomain(rid), cacheDuration),
        cachedFetch('networkInterfaces', async () => await getInterfaces(), cacheDuration),
        cachedFetch('networkSwitches', async () => await getSwitches(), cacheDuration),
        cachedFetch('networkObjects', async () => await getNetworkObjects(), cacheDuration)
    ]);

    return {
        rid,
        domain,
        interfaces,
        switches,
        networkObjects,
        vm
    };
}

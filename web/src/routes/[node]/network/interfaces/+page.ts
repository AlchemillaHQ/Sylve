import { getJails } from '$lib/api/jail/jail';
import { getVMs } from '$lib/api/vm/vm';
import { getInterfaces } from '$lib/api/network/iface';
import { getNetworkObjects } from '$lib/api/network/object';
import { getSwitches } from '$lib/api/network/switch';
import { getWireGuardClients } from '$lib/api/network/wireguard';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
    const [interfaces, objects, jails, vms, switches, wgClients] = await Promise.all([
        cachedFetch('networkInterfaces', async () => await getInterfaces(), SEVEN_DAYS),
        cachedFetch('networkObjects', async () => await getNetworkObjects(), SEVEN_DAYS),
        cachedFetch('jail-list', async () => await getJails(), SEVEN_DAYS),
        cachedFetch('vm-list', async () => await getVMs(), SEVEN_DAYS),
        cachedFetch('network-switches', async () => await getSwitches(), SEVEN_DAYS),
        cachedFetch('network-vpn-wireguard-clients', async () => await getWireGuardClients(), SEVEN_DAYS)
    ]);

    return {
        interfaces,
        objects,
        jails,
        vms,
        switches,
        wgClients
    };
}

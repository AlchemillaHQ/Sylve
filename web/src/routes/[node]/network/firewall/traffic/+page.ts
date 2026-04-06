import { getFirewallTrafficRules } from '$lib/api/network/firewall';
import { getInterfaces } from '$lib/api/network/iface';
import { getNetworkObjects } from '$lib/api/network/object';
import { getSwitches } from '$lib/api/network/switch';
import { getWireGuardClients } from '$lib/api/network/wireguard';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
    const [trafficRules, objects, interfaces, switches, wgClients] = await Promise.all([
        cachedFetch('firewall-traffic-rules', async () => await getFirewallTrafficRules(), SEVEN_DAYS),
        cachedFetch('network-objects', async () => await getNetworkObjects(), SEVEN_DAYS),
        cachedFetch('network-ifaces', async () => await getInterfaces(), SEVEN_DAYS),
        cachedFetch('network-switches', async () => await getSwitches(), SEVEN_DAYS),
        cachedFetch('network-vpn-wireguard-clients', async () => await getWireGuardClients(), SEVEN_DAYS)
    ]);

    return {
        trafficRules,
        objects,
        interfaces,
        switches,
        wgClients
    };
}

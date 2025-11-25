import { getInterfaces } from '$lib/api/network/iface';
import { getNetworkObjects } from '$lib/api/network/object.js';
import { getSwitches } from '$lib/api/network/switch';
import { getVMDomain, getVMs } from '$lib/api/vm/vm';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = 1000 * 60000;
	const rid = params.node;

	const [vms, domain, interfaces, switches, networkObjects] = await Promise.all([
		cachedFetch('vm-list', async () => getVMs(), cacheDuration),
		cachedFetch(`vm-domain-${rid}`, async () => getVMDomain(Number(rid)), cacheDuration),
		cachedFetch('networkInterfaces', async () => await getInterfaces(), cacheDuration),
		cachedFetch('networkSwitches', async () => await getSwitches(), cacheDuration),
		cachedFetch('networkObjects', async () => await getNetworkObjects(), cacheDuration)
	]);

	const vm = vms.find((vm) => vm.rid === Number(rid));

	return {
		rid,
		domain,
		interfaces,
		switches,
		networkObjects,
		vms,
		vm
	};
}

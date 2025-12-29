import { getRAMInfo } from '$lib/api/info/ram.js';
import { getPCIDevices, getPPTDevices } from '$lib/api/system/pci';
import { getVMDomain, getVMs } from '$lib/api/vm/vm';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const rid = Number(params.node);

	const cacheDuration = SEVEN_DAYS;
	const [vms, ram, domain, pciDevices, pptDevices] = await Promise.all([
		cachedFetch('vm-list', async () => await getVMs(), cacheDuration),
		cachedFetch('ramInfo', async () => await getRAMInfo('current'), cacheDuration),
		cachedFetch(`vmDomain-${rid}`, async () => await getVMDomain(rid), cacheDuration),
		cachedFetch('pciDevices', async () => await getPCIDevices(), cacheDuration),
		cachedFetch('pptDevices', async () => await getPPTDevices(), cacheDuration)
	]);

	const vm = vms.find((vm) => vm.rid === rid);

	return {
		rid,
		vm,
		vms,
		ram,
		domain,
		pciDevices: pciDevices || [],
		pptDevices: pptDevices || []
	};
}

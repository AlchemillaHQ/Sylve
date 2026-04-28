import { getRAMInfo } from '$lib/api/info/ram.js';
import { getPCIDevices, getPPTDevices } from '$lib/api/system/pci';
import { getVMs } from '$lib/api/vm/vm';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
    const rid = Number(params.rid);
    const cacheDuration = SEVEN_DAYS;

    const [vms, ram, pciDevices, pptDevices] = await Promise.all([
        cachedFetch('vm-list', async () => await getVMs(), cacheDuration),
        cachedFetch('ramInfo', async () => await getRAMInfo('current'), cacheDuration),
        cachedFetch('pciDevices', async () => await getPCIDevices(), cacheDuration),
        cachedFetch('pptDevices', async () => await getPPTDevices(), cacheDuration)
    ]);

    const vm = vms.find((vm) => vm.rid === rid);

    return {
        vm,
        vms,
        ram,
        pciDevices: pciDevices || [],
        pptDevices: pptDevices || []
    };
}

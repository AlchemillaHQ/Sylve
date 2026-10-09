import { getPCIDevices, getPPTDevices } from '$lib/api/system/pci';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;
	const [pciDevices, pptDevices] = await Promise.all([
		cachedFetch('pci-devices', async () => await getPCIDevices(), cacheDuration),
		cachedFetch('ppt-devices', async () => await getPPTDevices(), cacheDuration)
	]);

	return {
		pciDevices: isAPIResponse(pciDevices) ? [] : pciDevices,
		pptDevices: isAPIResponse(pptDevices) ? [] : pptDevices
	};
}

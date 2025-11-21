import { getVmById, getVMDomain, getVMs } from '$lib/api/vm/vm';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const vmId = Number(params.node);
	const cacheDuration = SEVEN_DAYS;
	const [vm, domain] = await Promise.all([
		cachedFetch(`vm-${vmId}`, async () => await getVmById(vmId, 'vmid'), cacheDuration),
		cachedFetch(`vm-domain-${vmId}`, async () => await getVMDomain(vmId), cacheDuration)
	]);

	return {
		vm,
		domain
	};
}

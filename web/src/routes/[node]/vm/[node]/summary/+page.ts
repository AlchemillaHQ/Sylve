import { getStats, getVmById, getVMDomain, getVMs } from '$lib/api/vm/vm';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const vmId = params.node;

	const [vm, domain, stats] = await Promise.all([
		cachedFetch(`vm-${vmId}`, async () => getVmById(Number(vmId), 'vmid'), cacheDuration),
		cachedFetch(`vm-domain-${vmId}`, async () => getVMDomain(Number(vmId)), cacheDuration),
		cachedFetch(`vm-stats-${vmId}`, async () => getStats(Number(vmId), 10), cacheDuration)
	]);

	return {
		vm: vm,
		domain: domain,
		stats: stats
	};
}

import { getStats, getVmById, getVMDomain, getVMs } from '$lib/api/vm/vm';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const rid = params.node;

	const [vm, domain, stats] = await Promise.all([
		cachedFetch(`vm-${rid}`, async () => getVmById(Number(rid), 'rid'), cacheDuration),
		cachedFetch(`vm-domain-${rid}`, async () => getVMDomain(Number(rid)), cacheDuration),
		cachedFetch(`vm-stats-${rid}`, async () => getStats(Number(rid), 'hourly'), cacheDuration)
	]);

	console.log('VM Summary Load:', { rid, vm, domain, stats });

	return {
		rid: Number(rid),
		vm: vm,
		domain: domain,
		stats: stats
	};
}

import { getVmById, getVMDomain } from '$lib/api/vm/vm';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const rid = Number(params.node);
	const cacheDuration = SEVEN_DAYS;
	const [vm, domain] = await Promise.all([
		cachedFetch(`vm-${rid}`, async () => await getVmById(rid, 'rid'), cacheDuration),
		cachedFetch(`vm-domain-${rid}`, async () => await getVMDomain(rid), cacheDuration)
	]);

	return {
		vm,
		domain
	};
}

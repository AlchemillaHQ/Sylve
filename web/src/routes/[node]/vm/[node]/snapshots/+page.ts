import { listVMSnapshots } from '$lib/api/vm/snapshots';
import { getVmById } from '$lib/api/vm/vm';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const rid = parseInt(params.node, 10);

	const [vm, snapshots] = await Promise.all([
		cachedFetch(`vm-${rid}`, async () => getVmById(rid, 'rid'), cacheDuration),
		cachedFetch(`vm-${rid}-snapshots`, async () => listVMSnapshots(rid), cacheDuration)
	]);

	return {
		rid,
		vm,
		snapshots
	};
}

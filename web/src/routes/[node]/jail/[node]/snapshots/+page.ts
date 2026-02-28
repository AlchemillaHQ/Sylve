import { getJailById } from '$lib/api/jail/jail';
import { listJailSnapshots } from '$lib/api/jail/snapshots';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const ctId = parseInt(params.node, 10);

	const [jail, snapshots] = await Promise.all([
		cachedFetch(`jail-${ctId}`, async () => getJailById(ctId, 'ctid'), cacheDuration),
		cachedFetch(`jail-${ctId}-snapshots`, async () => listJailSnapshots(ctId), cacheDuration)
	]);

	return {
		ctId,
		jail,
		snapshots
	};
}

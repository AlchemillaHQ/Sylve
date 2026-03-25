import { listJailSnapshots } from '$lib/api/jail/snapshots';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
    const cacheDuration = SEVEN_DAYS;
    const ctId = Number(params.ctid);

    const [snapshots] = await Promise.all([
        cachedFetch(`jail-${ctId}-snapshots`, async () => listJailSnapshots(ctId), cacheDuration)
    ]);

    return {
        ctId,
        snapshots
    };
}

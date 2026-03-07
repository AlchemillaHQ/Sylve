import { listVMSnapshots } from '$lib/api/vm/snapshots';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
    const cacheDuration = SEVEN_DAYS;
    const rid = parseInt(params.node, 10);

    const [snapshots] = await Promise.all([
        cachedFetch(`vm-${rid}-snapshots`, async () => listVMSnapshots(rid), cacheDuration)
    ]);

    return {
        rid,
        snapshots
    };
}

import { getVmById } from '$lib/api/vm/vm';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
    const rid = Number(params.rid);
    const cacheDuration = SEVEN_DAYS;
    const [vm] = await Promise.all([
        cachedFetch(`vm-${rid}`, async () => await getVmById(rid, 'rid'), cacheDuration)
    ]);

    return {
        rid,
        vm
    };
}

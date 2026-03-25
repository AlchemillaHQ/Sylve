import { getJailById } from '$lib/api/jail/jail';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
    const cacheDuration = SEVEN_DAYS;
    const ctId = Number(params.ctid);

    const [jail] = await Promise.all([
        cachedFetch(`jail-${ctId}`, async () => getJailById(ctId, 'ctid'), cacheDuration)
    ]);

    return {
        ctId: ctId,
        jail: jail
    };
}

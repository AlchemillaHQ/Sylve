import { getSimpleJailById } from '$lib/api/jail/jail';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
    const cacheDuration = SEVEN_DAYS;
    const ctId = parseInt(params.node);

    const [jail] = await Promise.all([
        cachedFetch(`simple-jail-${ctId}`, async () => getSimpleJailById(ctId, 'ctid'), cacheDuration),
    ]);

    return {
        ctId,
        jail,
    };
}

import { getJailById } from '$lib/api/jail/jail';
import { getBasicInfo } from '$lib/api/info/basic';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
    const cacheDuration = SEVEN_DAYS;
    const ctId = Number(params.ctid);

    const [jail, basicInfo] = await Promise.all([
        cachedFetch(`jail-${ctId}`, async () => getJailById(ctId, 'ctid'), cacheDuration),
        cachedFetch('basic-info', async () => getBasicInfo(), cacheDuration)
    ]);

    return {
        ctId: ctId,
        jail: jail,
        devFSDisabled: basicInfo?.devFSDisabled ?? false
    };
}

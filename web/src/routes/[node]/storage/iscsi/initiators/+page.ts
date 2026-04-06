import { getInitiators, getISCSIStatus } from '$lib/api/iscsi/initiator';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
    const cacheDuration = SEVEN_DAYS;
    const [initiators, status] = await Promise.all([
        cachedFetch('iscsi-initiators', async () => await getInitiators(), cacheDuration),
        cachedFetch('iscsi-status', async () => await getISCSIStatus(), cacheDuration)
    ]);

    return {
        initiators,
        status
    };
}

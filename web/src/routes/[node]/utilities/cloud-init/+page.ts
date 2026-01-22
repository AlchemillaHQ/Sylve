import { getTemplates } from '$lib/api/utilities/cloud-init';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
    const cacheDuration = SEVEN_DAYS;
    const [templates] = await Promise.all([
        cachedFetch('cloud-init-templates', async () => getTemplates(), cacheDuration, true)
    ]);

    return {
        templates
    };
}

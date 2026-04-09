import { listGroups } from '$lib/api/auth/groups';
import { listUsers } from '$lib/api/auth/local';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
    const cacheDuration = SEVEN_DAYS;
    const [users, groups] = await Promise.all([
        cachedFetch('users', async () => await listUsers(), cacheDuration),
        cachedFetch('groups', async () => await listGroups(), cacheDuration)
    ]);

    return {
        users: users || [],
        groups: groups || []
    };
}

import { listDisks } from '$lib/api/disk/disk';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;
	const [disks] = await Promise.all([
		cachedFetch('disk-list', async () => await listDisks(), cacheDuration)
	]);

	return {
		disks
	};
}

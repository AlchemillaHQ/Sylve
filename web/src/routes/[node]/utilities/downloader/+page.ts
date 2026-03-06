import { getDownloads } from '$lib/api/utilities/downloader';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;
	const [downloads] = await Promise.all([
		cachedFetch('downloads', async () => getDownloads(), cacheDuration)
	]);

	return {
		downloads
	};
}

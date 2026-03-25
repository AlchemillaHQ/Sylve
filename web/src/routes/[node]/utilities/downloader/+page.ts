import { getDownloadPaths, getDownloads } from '$lib/api/utilities/downloader';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const cacheDuration = SEVEN_DAYS;
	const [downloads, downloadPaths] = await Promise.all([
		cachedFetch('downloads', async () => getDownloads(), cacheDuration),
		getDownloadPaths()
	]);

	return {
		downloads,
		downloadPaths
	};
}

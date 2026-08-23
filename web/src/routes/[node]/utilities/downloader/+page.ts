import { getDownloadsResult } from '$lib/api/utilities/downloader';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const node = params.node;
	const result = await cachedFetch(
		'download-list',
		async () => getDownloadsResult({ hostname: node }),
		cacheDuration,
		false,
		node
	);

	return {
		node,
		downloads: isAPIResponse(result) ? [] : result,
		loadErrors: isAPIResponse(result) ? [result] : []
	};
}

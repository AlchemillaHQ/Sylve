import { getDownloadStorageChoices, getDownloadsResult } from '$lib/api/utilities/downloader';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, getAPIErrorText, isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const node = params.node;
	const [result, storageResult] = await Promise.all([
		cachedFetch(
			'download-list',
			async () => getDownloadsResult({ hostname: node }),
			cacheDuration,
			false,
			node
		),
		getDownloadStorageChoices({ hostname: node })
	]);

	return {
		node,
		downloads: isAPIResponse(result) ? [] : result,
		storageChoices: isAPIResponse(storageResult)
			? {
					choices: [{ storagePool: '', label: 'Default', available: true }],
					error: getAPIErrorText(storageResult, 'request_failed')
				}
			: storageResult,
		loadErrors: isAPIResponse(result) ? [result] : []
	};
}

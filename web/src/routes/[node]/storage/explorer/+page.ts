import { getFiles } from '$lib/api/system/file-explorer';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
	const result = await cachedFetch(
		'fx-files',
		async () => await getFiles(undefined, params.node),
		1,
		false,
		params.node
	);
	if (isAPIResponse(result)) {
		return {
			node: params.node,
			files: [],
			filesError: result
		};
	}

	return {
		node: params.node,
		files: result,
		filesError: null
	};
}

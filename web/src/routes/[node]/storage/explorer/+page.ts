import { getFiles } from '$lib/api/system/file-explorer';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	const result = await cachedFetch(
		'fx-files',
		async () => await getFiles(undefined, params.node),
		1,
		false,
		params.node
	);
	if (isAPIResponse(result)) {
		if (result.message === 'superuser_required') error(403, 'superuser_required');
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

import { getFiles } from '$lib/api/system/file-explorer';
import { ui } from '$lib/stores/interface.svelte';
import { isSuperuser, loadPermissions } from '$lib/stores/permissions.svelte';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	if (ui.zen) {
		await loadPermissions();
		return {
			node: params.node,
			files: [],
			filesError: null,
			denied: !isSuperuser()
		};
	}

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
			filesError: result,
			denied: false
		};
	}

	return {
		node: params.node,
		files: result,
		filesError: null,
		denied: false
	};
}

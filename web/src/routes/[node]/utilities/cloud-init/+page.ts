import { getTemplates } from '$lib/api/utilities/cloud-init';
import type { APIResponse } from '$lib/types/common';
import type { CloudInitTemplate } from '$lib/types/utilities/cloud-init';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
	const node = params.node;
	const result = await cachedFetch<CloudInitTemplate[] | APIResponse>(
		'cloud-init-templates',
		async () => getTemplates({ hostname: node }),
		SEVEN_DAYS,
		false,
		node
	);

	return {
		node,
		templates: isAPIResponse(result) ? [] : result,
		loadErrors: isAPIResponse(result) ? [result] : []
	};
}

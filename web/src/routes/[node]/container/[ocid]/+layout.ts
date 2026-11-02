import { getContainer } from '$lib/api/container/container';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import { error } from '@sveltejs/kit';

export const ssr = false;
export const prerender = false;
export const csr = true;

export async function load({ params }) {
	const ocId = Number(params.ocid);
	const node = String(params.node || '').trim();
	if (!Number.isSafeInteger(ocId) || ocId <= 0) error(404, 'Invalid container ID');
	if (!node) error(400, 'Invalid node hostname');

	const container = await cachedFetch(
		`container-${ocId}`,
		async () => getContainer(ocId, { hostname: node }),
		SEVEN_DAYS,
		false,
		node
	);
	if (isAPIResponse(container)) {
		error(
			container.message === 'container_not_found' ? 404 : 502,
			container.message || 'Unable to load container'
		);
	}

	return { node, ocId, container };
}

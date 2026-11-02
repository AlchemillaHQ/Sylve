import { getRegistryCredentials } from '$lib/api/container/resources';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const node = params.node;
	const credentials = await cachedFetch(
		'container-registries',
		() => getRegistryCredentials(node),
		SEVEN_DAYS,
		false,
		node
	);

	return { node, credentials };
}

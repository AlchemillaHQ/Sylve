import { basicSettingsOrFallback, getBasicSettings } from '$lib/api/system/settings';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const node = params.node;
	const settings = await cachedFetch(
		'system-basic-settings',
		() => getBasicSettings(node),
		SEVEN_DAYS,
		false,
		node
	);

	return { node, pools: basicSettingsOrFallback(settings).pools };
}

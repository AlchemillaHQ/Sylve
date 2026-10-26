import { getPools } from '$lib/api/zfs/pool';
import { basicSettingsOrFallback, getBasicSettings } from '$lib/api/system/settings';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const node = params.node;
	const [pools, basicSettings] = await Promise.all([
		cachedFetch('zfs-pools-full', () => getPools(true, node), SEVEN_DAYS, false, node),
		cachedFetch('system-basic-settings', () => getBasicSettings(node), SEVEN_DAYS, false, node)
	]);

	return {
		node,
		pools,
		basicSettings: basicSettingsOrFallback(basicSettings)
	};
}

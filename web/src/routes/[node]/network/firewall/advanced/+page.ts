import { getFirewallAdvancedSettings } from '$lib/api/network/firewall';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	const [advancedSettings] = await Promise.all([
		cachedFetch(
			'firewall-advanced-settings',
			async () => await getFirewallAdvancedSettings(),
			SEVEN_DAYS
		)
	]);

	return {
		advancedSettings
	};
}

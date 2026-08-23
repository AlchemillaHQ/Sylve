import { getFirewallAdvancedSettings } from '$lib/api/network/firewall';

export async function load() {
	return {
		advancedSettings: await getFirewallAdvancedSettings()
	};
}

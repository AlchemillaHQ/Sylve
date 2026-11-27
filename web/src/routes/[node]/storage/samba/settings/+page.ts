import { getInterfaces } from '$lib/api/network/iface';
import { getSwitches } from '$lib/api/network/switch';
import { getSambaConfig, getSambaConfigResult } from '$lib/api/samba/config';
import { getSambaStatus } from '$lib/api/samba/share';
import { ui } from '$lib/stores/interface.svelte';
import { canReadHost } from '$lib/stores/permissions.svelte';
import { SambaConfigSchema } from '$lib/types/samba/config';
import { SambaStatusSchema } from '$lib/types/samba/shares';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	const hostname = params.node;
	if (ui.zen) {
		if (!(await canReadHost(hostname))) {
			return {
				zen: true as const,
				hostname,
				denied: true,
				config: null,
				fresh: false,
				status: null,
				statusFresh: false
			};
		}
		let fresh = false;
		let statusFresh = false;
		const [config, status] = await Promise.all([
			cachedFetch(
				'samba-config',
				() => {
					fresh = true;
					return getSambaConfigResult({ hostname });
				},
				SEVEN_DAYS,
				false,
				hostname,
				SambaConfigSchema
			),
			cachedFetch(
				'samba-status',
				() => {
					statusFresh = true;
					return getSambaStatus({ hostname });
				},
				SEVEN_DAYS,
				false,
				hostname,
				SambaStatusSchema
			)
		]);
		return { zen: true as const, hostname, denied: false, config, fresh, status, statusFresh };
	}

	const cacheDuration = SEVEN_DAYS;
	const [interfaces, sambaConfig, switches] = await Promise.all([
		cachedFetch('network-interfaces', async () => await getInterfaces(), cacheDuration),
		cachedFetch('samba-config', async () => await getSambaConfig(), cacheDuration),
		cachedFetch('network-switches', async () => await getSwitches(), cacheDuration)
	]);

	return {
		zen: false as const,
		interfaces,
		sambaConfig,
		switches
	};
};

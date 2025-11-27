import { getRAMInfo } from '$lib/api/info/ram';
import { getJailById } from '$lib/api/jail/jail';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const ctId = parseInt(params.node, 10);

	const [jail, ram] = await Promise.all([
		cachedFetch(`jail-${ctId}`, async () => getJailById(ctId, 'ctid'), cacheDuration),
		cachedFetch('ram-info', async () => await getRAMInfo('current'), cacheDuration)
	]);

	return {
		jail: jail,
		ram: ram
	};
}

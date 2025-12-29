import { getCPUInfo } from '$lib/api/info/cpu.js';
import { getRAMInfo } from '$lib/api/info/ram.js';
import { getJailById, getJailStateById, getStats } from '$lib/api/jail/jail';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
	const ctId = parseInt(params.node, 10);
	const cacheDuration = SEVEN_DAYS;

	const [jail, state, stats, ramInfo, cpuInfo] = await Promise.all([
		cachedFetch(`jail-${ctId}`, async () => getJailById(ctId, 'ctid'), cacheDuration),
		cachedFetch(`jail-${ctId}-state`, async () => getJailStateById(ctId), cacheDuration),
		cachedFetch(`jail-${ctId}-stats`, async () => getStats(ctId, 'hourly'), cacheDuration),
		cachedFetch('system-ram-info', async () => getRAMInfo('current'), cacheDuration),
		cachedFetch('system-cpu-info', async () => getCPUInfo('current'), cacheDuration)
	]);

	return {
		ctId,
		jail: jail,
		state: state,
		stats: stats,
		ramInfo: ramInfo,
		cpuInfo: cpuInfo
	};
}

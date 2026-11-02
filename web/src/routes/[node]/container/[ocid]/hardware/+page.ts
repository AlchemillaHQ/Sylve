import { getCPUInfoResult } from '$lib/api/info/cpu.js';
import { getRAMInfoResult } from '$lib/api/info/ram';
import { ALWAYS_REFRESH, SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
	const node = String(params.node);
	const [ram, cpu] = await Promise.all([
		cachedFetch(
			'ram-info',
			async () => await getRAMInfoResult({ hostname: node }),
			ALWAYS_REFRESH,
			false,
			node
		),
		cachedFetch(
			'cpu-info',
			async () => await getCPUInfoResult({ hostname: node }),
			SEVEN_DAYS,
			false,
			node
		)
	]);

	return {
		ram: isAPIResponse(ram) ? null : ram,
		cpu: isAPIResponse(cpu) ? null : cpu
	};
}

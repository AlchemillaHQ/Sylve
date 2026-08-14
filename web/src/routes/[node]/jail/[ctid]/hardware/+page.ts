import { getCPUInfoResult } from '$lib/api/info/cpu.js';
import { getRAMInfoResult } from '$lib/api/info/ram';
import { getJailByCTID } from '$lib/api/jail/jail';
import type { APIResponse } from '$lib/types/common';
import type { Jail } from '$lib/types/jail/jail';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const ctId = Number(params.ctid);
	const node = String(params.node);
	if (!Number.isSafeInteger(ctId) || ctId <= 0) {
		error(404, 'Invalid jail CTID');
	}

	const [jailResult, ramResult, cpuResult] = await Promise.all([
		cachedFetch(
			`jail-${ctId}`,
			async () => getJailByCTID(ctId, { hostname: node, preserveErrors: true }),
			cacheDuration,
			false,
			node
		),
		cachedFetch(
			'ram-info',
			async () => await getRAMInfoResult({ hostname: node }),
			cacheDuration,
			false,
			node
		),
		cachedFetch(
			'cpu-info',
			async () => await getCPUInfoResult({ hostname: node }),
			cacheDuration,
			false,
			node
		)
	]);
	const jailError = (isAPIResponse(jailResult) ? jailResult : null) as APIResponse | null;
	const jail = (isAPIResponse(jailResult) ? null : jailResult) as Jail | null;

	return {
		node,
		ctId,
		jail,
		jailError,
		ram: isAPIResponse(ramResult) ? null : ramResult,
		ramError: (isAPIResponse(ramResult) ? ramResult : null) as APIResponse | null,
		cpu: isAPIResponse(cpuResult) ? null : cpuResult,
		cpuError: (isAPIResponse(cpuResult) ? cpuResult : null) as APIResponse | null
	};
}

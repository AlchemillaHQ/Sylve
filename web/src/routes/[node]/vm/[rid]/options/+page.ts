import { getCPUInfoResult } from '$lib/api/info/cpu';
import { getVmByIdResult } from '$lib/api/vm/vm';
import type { APIResponse } from '$lib/types/common';
import type { Architecture } from '$lib/types/info/cpu';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	const rid = Number(params.rid);
	const node = params.node;
	if (!Number.isSafeInteger(rid) || rid <= 0) {
		error(404, 'Invalid VM RID');
	}

	const [vmResult, cpuResult] = await Promise.all([
		cachedFetch(
			`vm-${rid}`,
			async () => getVmByIdResult(rid, { hostname: node }),
			SEVEN_DAYS,
			false,
			node
		),
		cachedFetch(
			'system-cpu-info',
			async () => getCPUInfoResult({ hostname: node }),
			SEVEN_DAYS,
			false,
			node
		)
	]);

	if (isAPIResponse(vmResult)) {
		const vmError = Array.isArray(vmResult.error) ? vmResult.error.join(', ') : vmResult.error;
		const detail = vmError || vmResult.message || 'Failed to load virtual machine';
		const notFound = /(?:not[_ ]found|record not found)/i.test(detail);
		error(notFound ? 404 : 502, detail);
	}

	return {
		node,
		rid,
		vm: vmResult,
		architecture: (isAPIResponse(cpuResult)
			? vmResult.bootRom === 'uboot'
				? 'arm64'
				: 'amd64'
			: cpuResult.architecture) as Architecture,
		loadErrors: (isAPIResponse(cpuResult) ? [cpuResult] : []) as APIResponse[]
	};
}

import { getNetworkObjects } from '$lib/api/network/object.js';
import { getSwitches } from '$lib/api/network/switch';
import { getVmByIdResult } from '$lib/api/vm/vm';
import type { APIResponse } from '$lib/types/common';
import { emptySwitchList, isSwitchList } from '$lib/types/network/switch';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	const rid = Number(params.rid);
	const node = params.node;
	if (!Number.isSafeInteger(rid) || rid <= 0) {
		error(404, 'Invalid VM RID');
	}

	const [vmResult, switchesResult, networkObjectsResult] = await Promise.all([
		cachedFetch(
			`vm-${rid}`,
			async () => getVmByIdResult(rid, { hostname: node }),
			SEVEN_DAYS,
			false,
			node
		),
		cachedFetch('network-switches', async () => getSwitches(node), SEVEN_DAYS, false, node),
		cachedFetch('network-objects', async () => getNetworkObjects(node), SEVEN_DAYS, false, node)
	]);

	if (isAPIResponse(vmResult)) {
		const vmError = Array.isArray(vmResult.error) ? vmResult.error.join(', ') : vmResult.error;
		error(502, vmError || vmResult.message || 'Failed to load virtual machine');
	}

	const loadErrors = [switchesResult, networkObjectsResult].filter(isAPIResponse) as APIResponse[];

	return {
		node,
		rid,
		vm: vmResult,
		switches: isSwitchList(switchesResult) ? switchesResult : emptySwitchList(),
		networkObjects: isAPIResponse(networkObjectsResult) ? [] : networkObjectsResult,
		loadErrors
	};
}

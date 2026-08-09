import { getRAMInfoResult } from '$lib/api/info/ram.js';
import { getPCIDevices, getPPTDevices } from '$lib/api/system/pci';
import { getVmByIdResult, getVMsResult } from '$lib/api/vm/vm';
import type { APIResponse } from '$lib/types/common';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	const rid = Number(params.rid);
	const node = params.node;
	if (!Number.isSafeInteger(rid) || rid <= 0) {
		error(404, 'Invalid VM RID');
	}

	const [vmsResult, vmResult, ramResult, pciDevicesResult, pptDevicesResult] = await Promise.all([
		cachedFetch('vm-list', async () => getVMsResult({ hostname: node }), SEVEN_DAYS, false, node),
		cachedFetch(
			`vm-${rid}`,
			async () => getVmByIdResult(rid, { hostname: node }),
			SEVEN_DAYS,
			false,
			node
		),
		cachedFetch(
			'system-ram-info',
			async () => getRAMInfoResult({ hostname: node }),
			SEVEN_DAYS,
			false,
			node
		),
		cachedFetch('pciDevices', async () => getPCIDevices(node), SEVEN_DAYS, false, node),
		cachedFetch('pptDevices', async () => getPPTDevices(node), SEVEN_DAYS, false, node)
	]);

	if (isAPIResponse(vmResult)) {
		const vmError = Array.isArray(vmResult.error) ? vmResult.error.join(', ') : vmResult.error;
		const detail = vmError || vmResult.message || 'Failed to load virtual machine';
		const notFound = /(?:not[_ ]found|record not found)/i.test(detail);
		error(notFound ? 404 : 502, detail);
	}

	const loadErrors = [vmsResult, ramResult, pciDevicesResult, pptDevicesResult].filter(
		isAPIResponse
	) as APIResponse[];

	return {
		node,
		rid,
		vm: vmResult,
		vms: isAPIResponse(vmsResult) ? [vmResult] : vmsResult,
		ram: isAPIResponse(ramResult) ? { total: 0, free: 0, usedPercent: 0 } : ramResult,
		pciDevices: isAPIResponse(pciDevicesResult) ? [] : pciDevicesResult,
		pptDevices: isAPIResponse(pptDevicesResult) ? [] : pptDevicesResult,
		loadErrors
	};
}

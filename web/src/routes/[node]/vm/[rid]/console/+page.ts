import { storage } from '$lib';
import { getVmByIdResult } from '$lib/api/vm/vm';
import { isAPIResponse, updateCache } from '$lib/utils/http';
import { sha256 } from '$lib/utils/string';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	const rid = Number(params.rid);
	const node = String(params.node || '').trim();
	if (!Number.isSafeInteger(rid) || rid <= 0) error(400, 'Invalid VM RID');
	if (!node) error(400, 'Invalid node hostname');

	const vm = await getVmByIdResult(rid, { hostname: node });
	if (isAPIResponse(vm)) {
		error(vm.message === 'vm_not_found' ? 404 : 502, vm.message || 'Unable to load VM');
	}
	await updateCache(`vm-${rid}`, vm, node);
	const hash = await sha256(storage.token || '', 1);

	return {
		node,
		vm,
		rid,
		hash
	};
}

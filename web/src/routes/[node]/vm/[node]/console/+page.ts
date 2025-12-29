import { storage } from '$lib';
import { getVmById, getVMDomain } from '$lib/api/vm/vm';
import { sha256 } from '$lib/utils/string';

export async function load({ params }) {
	const vm = await getVmById(Number(params.node), 'rid');
	const domain = await getVMDomain(vm.rid);
	const hash = await sha256(storage.token || '', 1);

	return {
		vm: vm,
		domain: domain,
		rid: vm.rid,
		hash: hash
	};
}

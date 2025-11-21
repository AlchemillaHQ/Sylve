import { storage } from '$lib';
import { getVmById, getVMDomain } from '$lib/api/vm/vm';
import { sha256 } from '$lib/utils/string';

export async function load({ params }) {
	const vm = await getVmById(Number(params.node), 'vmid');
	const domain = await getVMDomain(vm.vmId);
	const hash = await sha256(storage.token || '', 1);

	return {
		vm: vm,
		domain: domain,
		vmId: vm.vmId,
		hash: hash
	};
}

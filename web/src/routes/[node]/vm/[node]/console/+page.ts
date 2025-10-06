import { getVMDomain, getVMs } from '$lib/api/vm/vm';
import { store as token } from '$lib/stores/auth';
import { sha256 } from '$lib/utils/string';
import { get } from 'svelte/store';

export async function load({ params }) {
	const vms = (await getVMs()) || [];
	const vm = vms.find((vm) => vm.vmId === Number(params.node));
	const domain = await getVMDomain(vm?.vmId || 0);

	let id = 0;
	let port = 0;
	let password = '';
	let hash = await sha256(get(token), 1);
	let serial = false;
	let vnc = false;

	if (vm) {
		id = vm.vmId;
		port = vm.vncPort;
		password = vm.vncPassword;
		serial = vm.serial;
		vnc = vm.vncEnabled;
	}

	return {
		id,
		vnc,
		serial,
		port,
		password,
		domain,
		hash
	};
}

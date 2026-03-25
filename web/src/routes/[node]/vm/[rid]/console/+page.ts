import { storage } from '$lib';
import { getVmById, getVMDomain } from '$lib/api/vm/vm';
import { sha256 } from '$lib/utils/string';

export async function load({ params }) {
    const rid = Number(params.rid);

    const vm = await getVmById(rid, 'rid');
    const domain = await getVMDomain(rid);
    const hash = await sha256(storage.token || '', 1);

    return {
        vm: vm,
        domain: domain,
        rid: rid,
        hash: hash
    };
}

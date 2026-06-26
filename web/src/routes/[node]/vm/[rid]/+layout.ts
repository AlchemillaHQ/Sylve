import { getSimpleVMById, getVMDomain } from '$lib/api/vm/vm';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export const ssr = false;
export const prerender = false;
export const csr = true;

export async function load({ params }) {
	const rid = Number(params.rid);

	const [vm, domain] = await Promise.all([
		cachedFetch(
			`simple-vm-${rid}`,
			async () => {
				const result = await getSimpleVMById(rid, 'rid');
				return isAPIResponse(result) ? null : result;
			},
			SEVEN_DAYS
		),
		cachedFetch(
			`vm-domain-${rid}`,
			async () => {
				const result = await getVMDomain(rid);
				return isAPIResponse(result) ? null : result;
			},
			SEVEN_DAYS
		)
	]);

	return {
		rid,
		vm,
		domain
	};
}

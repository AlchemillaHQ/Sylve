import { getSimpleVMById, getVMDomain } from '$lib/api/vm/vm';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import { error } from '@sveltejs/kit';

export const ssr = false;
export const prerender = false;
export const csr = true;

export async function load({ params }) {
	const rid = Number(params.rid);
	const node = String(params.node || '').trim();
	if (!Number.isSafeInteger(rid) || rid <= 0) error(400, 'Invalid VM RID');
	if (!node) error(400, 'Invalid node hostname');

	const [vm, domain] = await Promise.all([
		cachedFetch(
			`simple-vm-${rid}`,
			async () => {
				const result = await getSimpleVMById(rid, { hostname: node });
				return isAPIResponse(result) ? null : result;
			},
			SEVEN_DAYS,
			false,
			node
		),
		cachedFetch(
			`vm-domain-${rid}`,
			async () => {
				const result = await getVMDomain(rid, { hostname: node });
				return isAPIResponse(result) ? null : result;
			},
			SEVEN_DAYS,
			false,
			node
		)
	]);

	return {
		node,
		rid,
		vm,
		domain
	};
}

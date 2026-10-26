import type { APIResponse } from '$lib/types/common';
import { IfaceSchema, type Iface } from '$lib/types/network/iface';
import { apiRequest, type NodeAPIDataRequestOptions } from '$lib/utils/http';

export async function getInterfaces(
	options?: NodeAPIDataRequestOptions
): Promise<Iface[] | APIResponse> {
	return await apiRequest('/network/interface', IfaceSchema.array(), 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

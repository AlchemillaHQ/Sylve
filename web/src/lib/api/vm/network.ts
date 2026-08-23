import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { VMNetworkSchema, type VMNetwork } from '$lib/types/vm/vm';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';

export type NetworkAttachRequest = {
	switchName: string;
	emulation: 'virtio' | 'e1000';
	macId?: number;
};

export type NetworkUpdateRequest = {
	switchName?: string;
	emulation?: 'virtio' | 'e1000';
	macId?: number;
	enable?: boolean;
};

export async function detachNetwork(
	rid: number,
	networkId: number,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/networks/${networkId}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		{
			...options,
			preserveErrors: true
		}
	);
}

export async function attachNetwork(
	rid: number,
	request: NetworkAttachRequest,
	options?: NodeAPIRequestOptions
): Promise<VMNetwork | APIResponse> {
	return await apiRequest(`/vm/${rid}/networks`, VMNetworkSchema, 'POST', request, {
		...options,
		preserveErrors: true
	});
}

export async function updateNetwork(
	rid: number,
	networkId: number,
	request: NetworkUpdateRequest,
	options?: NodeAPIRequestOptions
): Promise<VMNetwork | APIResponse> {
	return await apiRequest(`/vm/${rid}/networks/${networkId}`, VMNetworkSchema, 'PATCH', request, {
		...options,
		preserveErrors: true
	});
}

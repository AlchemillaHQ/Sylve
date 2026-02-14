import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';

export async function detachNetwork(rid: number, switchId: number): Promise<APIResponse> {
	return await apiRequest(`/vm/network/detach`, APIResponseSchema, 'POST', {
		rid,
		networkId: switchId
	});
}

export async function attachNetwork(
	rid: number,
	switchName: string,
	emulation: string,
	macId: number
): Promise<APIResponse> {
	return await apiRequest(`/vm/network/attach`, APIResponseSchema, 'POST', {
		rid,
		switchName,
		emulation,
		macId
	});
}

export async function updateNetwork(
	networkId: number,
	switchName: string,
	emulation: string,
	macId: number
): Promise<APIResponse> {
	return await apiRequest(`/vm/network/update`, APIResponseSchema, 'PUT', {
		networkId,
		switchName,
		emulation,
		macId
	});
}

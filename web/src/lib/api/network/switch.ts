import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { SwitchListSchema, type SwitchList } from '$lib/types/network/switch';
import { apiRequest } from '$lib/utils/http';

export async function getSwitches(): Promise<SwitchList> {
	return await apiRequest('/network/switch', SwitchListSchema, 'GET');
}

export async function createSwitch(
	name: string,
	mtu: number,
	vlan: number,
	network4: number,
	gateway4: number,
	network6: number,
	gateway6: number,
	privateSw: boolean,
	dhcp: boolean,
	ports: string[],
	disableIPv6: boolean,
	slaac: boolean
): Promise<APIResponse> {
	const body = {
		name,
		mtu,
		vlan,
		network4,
		gateway4,
		network6,
		gateway6,
		private: privateSw,
		ports,
		dhcp,
		disableIPv6,
		slaac
	};

	return await apiRequest('/network/switch/standard', APIResponseSchema, 'POST', body);
}

export async function deleteSwitch(id: number): Promise<APIResponse> {
	return await apiRequest(`/network/switch/standard/${id}`, APIResponseSchema, 'DELETE');
}

export async function updateSwitch(
	id: number,
	mtu: number,
	vlan: number,
	network4: number,
	gateway4: number,
	network6: number,
	gateway6: number,
	privateSw: boolean,
	ports: string[],
	disableIPv6: boolean,
	slaac: boolean,
	dhcp: boolean
): Promise<APIResponse> {
	const body = {
		id,
		mtu,
		vlan,
		network4,
		gateway4,
		network6,
		gateway6,
		private: privateSw,
		ports,
		disableIPv6,
		slaac,
		dhcp
	};

	return await apiRequest('/network/switch/standard', APIResponseSchema, 'PUT', body);
}

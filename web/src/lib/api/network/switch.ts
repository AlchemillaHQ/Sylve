import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { SwitchListSchema, type SwitchList } from '$lib/types/network/switch';
import { apiRequest } from '$lib/utils/http';

export async function getSwitches(): Promise<SwitchList> {
	return await apiRequest('/network/switch', SwitchListSchema, 'GET');
}

export async function createManualSwitch(name: string, bridge: string): Promise<APIResponse> {
	const body = {
		name,
		bridge
	};

	return await apiRequest('/network/manual-switch', APIResponseSchema, 'POST', body);
}

export async function deleteManualSwitch(id: number): Promise<APIResponse> {
	return await apiRequest(`/network/manual-switch/${id}`, APIResponseSchema, 'DELETE');
}

export type SwitchManualAddresses = {
	network4: string;
	gateway4: string;
	network6: string;
	gateway6: string;
};

const emptyManualAddresses: SwitchManualAddresses = {
	network4: '',
	gateway4: '',
	network6: '',
	gateway6: ''
};

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
	slaac: boolean,
	defaultRoute: boolean,
	manual: SwitchManualAddresses = emptyManualAddresses
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
		slaac,
		defaultRoute,
		network4Manual: manual.network4,
		gateway4Manual: manual.gateway4,
		network6Manual: manual.network6,
		gateway6Manual: manual.gateway6
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
	dhcp: boolean,
	defaultRoute: boolean,
	manual: SwitchManualAddresses = emptyManualAddresses
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
		dhcp,
		defaultRoute,
		network4Manual: manual.network4,
		gateway4Manual: manual.gateway4,
		network6Manual: manual.network6,
		gateway6Manual: manual.gateway6
	};

	return await apiRequest('/network/switch/standard', APIResponseSchema, 'PUT', body);
}

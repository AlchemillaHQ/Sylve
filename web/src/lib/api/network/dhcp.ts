import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	DHCPConfigSchema,
	DHCPRangeSchema,
	type DHCPConfig,
	type DHCPRange
} from '$lib/types/network/dhcp';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getDHCPConfig(): Promise<DHCPConfig> {
	return await apiRequest('/network/dhcp/config', DHCPConfigSchema, 'GET');
}

export async function updateDHCPConfig(
	standardSwitches: number[],
	manualSwitches: number[],
	dnsServers: string[],
	domain: string,
	expandHosts?: boolean
): Promise<APIResponse> {
	const body: {
		standardSwitches: number[];
		manualSwitches: number[];
		dnsServers: string[];
		domain: string;
		expandHosts?: boolean;
	} = {
		standardSwitches,
		manualSwitches,
		dnsServers,
		domain,
		expandHosts
	};

	return await apiRequest('/network/dhcp/config', APIResponseSchema, 'PUT', body);
}

export async function getDHCPRanges(): Promise<DHCPRange[]> {
	return await apiRequest('/network/dhcp/range', z.array(DHCPRangeSchema), 'GET');
}

export async function createDHCPRange(
	ipType: 'ipv4' | 'ipv6',
	startIp: string,
	endIp: string,
	expiry: number,
	raOnly: boolean,
	slaac: boolean,
	standardSwitchId?: number,
	manualSwitchId?: number
): Promise<APIResponse> {
	return await apiRequest('/network/dhcp/range', APIResponseSchema, 'POST', {
		type: ipType,
		startIp,
		endIp,
		expiry,
		standardSwitch: standardSwitchId,
		manualSwitch: manualSwitchId,
		raOnly,
		slaac
	});
}

export async function updateDHCPRange(
	type: 'ipv4' | 'ipv6',
	id: number,
	startIp: string,
	endIp: string,
	expiry: number,
	raOnly: boolean,
	slaac: boolean,
	standardSwitchId?: number,
	manualSwitchId?: number
): Promise<APIResponse> {
	return await apiRequest(`/network/dhcp/range/${id}`, APIResponseSchema, 'PUT', {
		type,
		id,
		startIp,
		endIp,
		expiry,
		raOnly,
		slaac,
		standardSwitch: standardSwitchId,
		manualSwitch: manualSwitchId
	});
}

export async function deleteDHCPRange(id: number): Promise<APIResponse> {
	return await apiRequest(`/network/dhcp/range/${id}`, APIResponseSchema, 'DELETE');
}

import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	JailLogsSchema,
	JailSchema,
	JailStateSchema,
	JailStatSchema,
	SimpleJailSchema,
	type CreateData,
	type Jail,
	type JailLogs,
	type JailStat,
	type JailState,
	type SimpleJail
} from '$lib/types/jail/jail';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function newJail(data: CreateData): Promise<APIResponse> {
	return await apiRequest('/jail', APIResponseSchema, 'POST', {
		name: data.name,
		node: data.node,
		ctId: parseInt(data.id.toString(), 10),
		description: data.description,
		dataset: data.storage.dataset,
		base: data.storage.base,
		switchName: data.network.switch,
		dhcp: data.network.dhcp,
		slaac: data.network.slaac,
		inheritIPv4: data.network.inheritIPv4,
		inheritIPv6: data.network.inheritIPv6,
		ipv4: data.network.ipv4,
		ipv4Gw: data.network.ipv4Gateway,
		ipv6: data.network.ipv6,
		ipv6Gw: data.network.ipv6Gateway,
		mac: data.network.mac,
		resourceLimits: data.hardware.resourceLimits,
		cores: parseInt(data.hardware.cpuCores.toString(), 10),
		memory: parseInt(data.hardware.ram.toString(), 10),
		startAtBoot: data.hardware.startAtBoot,
		startOrder: data.hardware.bootOrder
	});
}

export async function getSimpleJails(): Promise<SimpleJail[]> {
	return await apiRequest('/jail/simple', z.array(SimpleJailSchema), 'GET');
}

export async function getJails(): Promise<Jail[]> {
	return await apiRequest('/jail', z.array(JailSchema), 'GET');
}

export async function deleteJail(
	ctId: number,
	deleteMacs: boolean,
	deleteRootFs: boolean
): Promise<APIResponse> {
	return await apiRequest(
		`/jail/${ctId}?deletemacs=${deleteMacs}&deleterootfs=${deleteRootFs}`,
		APIResponseSchema,
		'DELETE'
	);
}

export async function getJailStates(): Promise<JailState[]> {
	return await apiRequest('/jail/state', z.array(JailStateSchema), 'GET');
}

export async function jailAction(ctId: number, action: string): Promise<APIResponse> {
	return await apiRequest(`/jail/action/${action}/${ctId}`, APIResponseSchema, 'POST');
}

export async function updateDescription(id: number, description: string): Promise<APIResponse> {
	return await apiRequest('/jail/description', APIResponseSchema, 'PUT', {
		id,
		description
	});
}

export async function getJailLogs(id: number, start: boolean): Promise<JailLogs> {
	return await apiRequest(`/jail/${id}/logs?start=${start}`, JailLogsSchema, 'GET');
}

export async function getStats(ctId: number, limit: number): Promise<JailStat[]> {
	return await apiRequest(`/jail/stats/${ctId}/${limit}`, z.array(JailStatSchema), 'GET');
}

export async function inheritHostNetwork(
	ctId: number,
	ipv4: boolean,
	ipv6: boolean
): Promise<APIResponse> {
	return await apiRequest('/jail/network/inheritance', APIResponseSchema, 'POST', {
		ctId,
		ipv4,
		ipv6
	});
}

export async function disinheritHostNetwork(ctId: number): Promise<APIResponse> {
	return await apiRequest(`/jail/network/disinherit/${ctId}`, APIResponseSchema, 'DELETE');
}

export async function addNetwork(
	ctId: number,
	switchName: string,
	macId: number,
	ip4: number,
	ip4gw: number,
	ip6: number,
	ip6gw: number,
	dhcp: boolean,
	slaac: boolean
): Promise<APIResponse> {
	return await apiRequest('/jail/network', APIResponseSchema, 'POST', {
		ctId,
		switchName,
		macId,
		ip4,
		ip4gw,
		ip6,
		ip6gw,
		dhcp,
		slaac
	});
}

export async function deleteNetwork(ctId: number, networkId: number): Promise<APIResponse> {
	return await apiRequest(`/jail/network/${ctId}/${networkId}`, APIResponseSchema, 'DELETE');
}

export async function updateResourceLimits(ctId: number, enabled: boolean): Promise<APIResponse> {
	return await apiRequest(
		`/jail/resource-limits/${ctId}?enabled=${enabled}`,
		APIResponseSchema,
		'PUT'
	);
}

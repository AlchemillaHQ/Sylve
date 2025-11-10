import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	SimpleVmSchema,
	VMDomainSchema,
	VMSchema,
	VMStatSchema,
	type CreateData,
	type SimpleVm,
	type VM,
	type VMDomain,
	type VMStat
} from '$lib/types/vm/vm';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getVMs(): Promise<VM[]> {
	return await apiRequest('/vm', z.array(VMSchema), 'GET');
}

export async function getSimpleVMs(): Promise<SimpleVm[]> {
	return await apiRequest('/vm/simple', z.array(SimpleVmSchema), 'GET');
}

export async function newVM(data: CreateData): Promise<APIResponse> {
	return await apiRequest('/vm', APIResponseSchema, 'POST', {
		name: data.name,
		node: data.node,
		description: data.description,
		vmId: parseInt(data.id.toString(), 10),
		iso: data.storage.iso,
		storagePool: data.storage.pool,
		storageType: data.storage.type,
		storageSize: data.storage.size,
		storageEmulationType: data.storage.emulation,
		switchName: data.network.switch,
		switchEmulationType: data.network.emulation,
		macId: Number(data.network.mac) || 0,
		cpuSockets: parseInt(data.hardware.sockets.toString(), 10),
		cpuCores: parseInt(data.hardware.cores.toString(), 10),
		cpuThreads: parseInt(data.hardware.threads.toString(), 10),
		cpuPinning: data.hardware.pinnedCPUs,
		ram: parseInt(data.hardware.memory.toString(), 10),
		pciDevices: data.hardware.passthroughIds,
		tpmEmulation: data.advanced.tpmEmulation,
		serial: data.advanced.serial,
		vncPort: data.advanced.vncPort,
		vncPassword: data.advanced.vncPassword,
		vncWait: data.advanced.vncWait,
		vncResolution: data.advanced.vncResolution,
		startAtBoot: data.advanced.startAtBoot,
		bootOrder: parseInt(data.advanced.bootOrder.toString(), 10),
		timeOffset: data.advanced.timeOffset
	});
}

export async function deleteVM(
	id: number,
	deleteMacs: boolean,
	deleteRawDisks: boolean,
	deleteVolumes: boolean
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${id}?deletemacs=${deleteMacs}&deleterawdisks=${deleteRawDisks}&deletevolumes=${deleteVolumes}`,
		APIResponseSchema,
		'DELETE'
	);
}

export async function getVMDomain(id: number | string): Promise<VMDomain> {
	return await apiRequest(`/vm/domain/${id}`, VMDomainSchema, 'GET');
}

export async function actionVm(id: number | string, action: string): Promise<APIResponse> {
	return await apiRequest(`/vm/${action}/${id}`, APIResponseSchema, 'POST');
}

export async function getStats(vmId: number, limit: number): Promise<VMStat[]> {
	return await apiRequest(`/vm/stats/${vmId}/${limit}`, z.array(VMStatSchema), 'GET');
}

export async function updateDescription(id: number, description: string): Promise<APIResponse> {
	return await apiRequest(`/vm/description`, APIResponseSchema, 'PUT', {
		id,
		description
	});
}

export async function modifyWoL(vmid: number, enabled: boolean): Promise<APIResponse> {
	return await apiRequest(`/vm/options/wol/${vmid}`, APIResponseSchema, 'PUT', {
		enabled
	});
}

export async function modifyBootOrder(
	vmid: number,
	startAtBoot: boolean,
	bootOrder: number
): Promise<APIResponse> {
	return await apiRequest(`/vm/options/boot-order/${vmid}`, APIResponseSchema, 'PUT', {
		startAtBoot,
		bootOrder
	});
}

export async function modifyClockOffset(
	vmid: number,
	timeOffset: 'localtime' | 'utc'
): Promise<APIResponse> {
	return await apiRequest(`/vm/options/clock/${vmid}`, APIResponseSchema, 'PUT', {
		timeOffset
	});
}

export async function modifySerialConsole(vmid: number, enabled: boolean): Promise<APIResponse> {
	return await apiRequest(`/vm/options/serial-console/${vmid}`, APIResponseSchema, 'PUT', {
		enabled
	});
}

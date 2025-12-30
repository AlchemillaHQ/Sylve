import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import type { CPUPin, VM } from '$lib/types/vm/vm';
import { apiRequest } from '$lib/utils/http';

export async function modifyCPU(
	rid: number,
	cpuSockets: number,
	cpuCores: number,
	cpuThreads: number,
	cpuPinning: CPUPin[]
): Promise<APIResponse> {
	return await apiRequest(`/vm/hardware/cpu/${rid}`, APIResponseSchema, 'PUT', {
		cpuSockets,
		cpuCores,
		cpuThreads,
		cpuPinning
	});
}

export async function modifyRAM(rid: number, ram: number): Promise<APIResponse> {
	return await apiRequest(`/vm/hardware/ram/${rid}`, APIResponseSchema, 'PUT', {
		ram
	});
}

export async function modifyVNC(
	rid: number,
	vncEnabled: boolean,
	vncPort: number,
	vncResolution: string,
	vncPassword: string,
	vncWait: boolean
): Promise<APIResponse> {
	return await apiRequest(`/vm/hardware/vnc/${rid}`, APIResponseSchema, 'PUT', {
		vncEnabled,
		vncPort,
		vncResolution,
		vncPassword,
		vncWait
	});
}

export async function modifyPPT(rid: number, pciDevices: number[]): Promise<APIResponse> {
	return await apiRequest(`/vm/hardware/ppt/${rid}`, APIResponseSchema, 'PUT', {
		pciDevices
	});
}

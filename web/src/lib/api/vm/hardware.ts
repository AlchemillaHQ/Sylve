import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import type { CPUPin } from '$lib/types/vm/vm';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';

export async function modifyCPU(
	rid: number,
	cpuSockets: number,
	cpuCores: number,
	cpuThreads: number,
	cpuPinning: CPUPin[],
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/hardware/cpu`,
		APIResponseSchema,
		'PUT',
		{
			cpuSockets,
			cpuCores,
			cpuThreads,
			cpuPinning
		},
		{
			...options,
			preserveErrors: true
		}
	);
}

export async function modifyRAM(
	rid: number,
	ram: number,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/hardware/ram`,
		APIResponseSchema,
		'PUT',
		{ ram },
		{
			...options,
			preserveErrors: true
		}
	);
}

export async function modifyVNC(
	rid: number,
	vncEnabled: boolean,
	vncPort: number,
	vncBind: string,
	vncResolution: string,
	vncPassword: string,
	vncWait: boolean,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/hardware/vnc`,
		APIResponseSchema,
		'PUT',
		{
			vncEnabled,
			vncPort,
			vncBind,
			vncResolution,
			vncPassword,
			vncWait
		},
		{
			...options,
			preserveErrors: true
		}
	);
}

export async function modifyPPT(
	rid: number,
	pciDevices: number[],
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/hardware/pci-devices`,
		APIResponseSchema,
		'PUT',
		{ pciDevices },
		{
			...options,
			preserveErrors: true
		}
	);
}

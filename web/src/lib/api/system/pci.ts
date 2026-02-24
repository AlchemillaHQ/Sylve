import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	PCIDeviceSchema,
	PPTDeviceSchema,
	type PCIDevice,
	type PPTDevice
} from '$lib/types/system/pci';
import { apiRequest } from '$lib/utils/http';

export async function getPCIDevices(): Promise<PCIDevice[]> {
	return await apiRequest('/system/pci-devices', PCIDeviceSchema.array(), 'GET');
}

export async function getPPTDevices(): Promise<PPTDevice[]> {
	return await apiRequest('/system/ppt-devices', PPTDeviceSchema.array(), 'GET');
}

export async function addPPTDevice(domain: string, deviceID: string): Promise<APIResponse> {
	return await apiRequest('/system/ppt-devices', APIResponseSchema, 'POST', { domain, deviceID });
}

export async function preparePPTDevice(domain: string, deviceID: string): Promise<APIResponse> {
	return await apiRequest('/system/ppt-devices/prepare', APIResponseSchema, 'POST', {
		domain,
		deviceID
	});
}

export async function removePPTDevice(deviceID: string): Promise<APIResponse> {
	return await apiRequest(`/system/ppt-devices/${deviceID}`, APIResponseSchema, 'DELETE');
}

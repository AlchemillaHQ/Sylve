import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { DataCenterSchema, type DataCenter } from '$lib/types/datacenter/cluster';
import { apiRequest } from '$lib/utils/http';

export async function getCluster(): Promise<DataCenter> {
	const response = await apiRequest('/datacenter/cluster', DataCenterSchema, 'GET');
	return response;
}

export async function createCluster(): Promise<APIResponse> {
	return await apiRequest('/datacenter/cluster', APIResponseSchema, 'POST');
}

import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { VMSnapshotSchema, type VMSnapshot } from '$lib/types/vm/snapshots';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function listVMSnapshots(rid: number): Promise<VMSnapshot[]> {
	return await apiRequest(`/vm/snapshots/${rid}`, z.array(VMSnapshotSchema), 'GET');
}

export async function createVMSnapshot(
	rid: number,
	name: string,
	description: string
): Promise<APIResponse> {
	return await apiRequest(`/vm/snapshots/${rid}`, APIResponseSchema, 'POST', {
		name,
		description
	});
}

export async function rollbackVMSnapshot(rid: number, snapshotId: number): Promise<APIResponse> {
	return await apiRequest(`/vm/snapshots/${rid}/${snapshotId}/rollback`, APIResponseSchema, 'POST', {});
}

export async function deleteVMSnapshot(rid: number, snapshotId: number): Promise<APIResponse> {
	return await apiRequest(`/vm/snapshots/${rid}/${snapshotId}`, APIResponseSchema, 'DELETE');
}

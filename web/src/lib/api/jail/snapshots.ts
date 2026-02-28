import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { JailSnapshotSchema, type JailSnapshot } from '$lib/types/jail/snapshots';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function listJailSnapshots(ctId: number): Promise<JailSnapshot[]> {
	return await apiRequest(`/jail/snapshots/${ctId}`, z.array(JailSnapshotSchema), 'GET');
}

export async function createJailSnapshot(
	ctId: number,
	name: string,
	description: string
): Promise<APIResponse> {
	return await apiRequest(`/jail/snapshots/${ctId}`, APIResponseSchema, 'POST', {
		name,
		description
	});
}

export async function rollbackJailSnapshot(ctId: number, snapshotId: number): Promise<APIResponse> {
	return await apiRequest(
		`/jail/snapshots/${ctId}/${snapshotId}/rollback`,
		APIResponseSchema,
		'POST',
		{}
	);
}

export async function deleteJailSnapshot(ctId: number, snapshotId: number): Promise<APIResponse> {
	return await apiRequest(`/jail/snapshots/${ctId}/${snapshotId}`, APIResponseSchema, 'DELETE');
}

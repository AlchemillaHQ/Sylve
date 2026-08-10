import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	JailSnapshotRollbackResultSchema,
	JailSnapshotSchema,
	type JailSnapshot,
	type JailSnapshotRollbackResult
} from '$lib/types/jail/snapshots';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function listJailSnapshots(
	ctId: number,
	options: NodeAPIRequestOptions = {}
): Promise<JailSnapshot[] | APIResponse> {
	return await apiRequest(
		`/jail/${ctId}/snapshots`,
		z.array(JailSnapshotSchema),
		'GET',
		undefined,
		{
			...options,
			preserveErrors: true
		}
	);
}

export async function createJailSnapshot(
	ctId: number,
	name: string,
	description: string,
	options: NodeAPIRequestOptions = {}
): Promise<JailSnapshot | APIResponse> {
	return await apiRequest(
		`/jail/${ctId}/snapshots`,
		JailSnapshotSchema,
		'POST',
		{
			name,
			description
		},
		{ ...options, preserveErrors: true }
	);
}

export async function rollbackJailSnapshot(
	ctId: number,
	snapshotId: number,
	options: NodeAPIRequestOptions = {}
): Promise<JailSnapshotRollbackResult | APIResponse> {
	return await apiRequest(
		`/jail/${ctId}/snapshots/${snapshotId}/rollback`,
		JailSnapshotRollbackResultSchema,
		'POST',
		{},
		{ ...options, preserveErrors: true }
	);
}

export async function deleteJailSnapshot(
	ctId: number,
	snapshotId: number,
	options: NodeAPIRequestOptions = {}
): Promise<APIResponse> {
	return await apiRequest(
		`/jail/${ctId}/snapshots/${snapshotId}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		{ ...options, preserveErrors: true }
	);
}

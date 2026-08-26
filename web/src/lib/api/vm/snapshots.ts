import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	VMSnapshotRollbackResultSchema,
	VMSnapshotSchema,
	type VMSnapshot,
	type VMSnapshotRollbackResult
} from '$lib/types/vm/snapshots';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function listVMSnapshots(
	rid: number,
	options: NodeAPIRequestOptions = {}
): Promise<VMSnapshot[] | APIResponse> {
	return await apiRequest(
		'/vm/' + rid + '/snapshots',
		z.array(VMSnapshotSchema),
		'GET',
		undefined,
		{
			...options,
			preserveErrors: true
		}
	);
}

export async function createVMSnapshot(
	rid: number,
	name: string,
	description: string,
	options: NodeAPIRequestOptions = {}
): Promise<VMSnapshot | APIResponse> {
	return await apiRequest(
		'/vm/' + rid + '/snapshots',
		VMSnapshotSchema,
		'POST',
		{
			name,
			description
		},
		{ ...options, preserveErrors: true }
	);
}

export async function rollbackVMSnapshot(
	rid: number,
	snapshotId: number,
	options: NodeAPIRequestOptions = {}
): Promise<VMSnapshotRollbackResult | APIResponse> {
	return await apiRequest(
		'/vm/' + rid + '/snapshots/' + snapshotId + '/rollback',
		VMSnapshotRollbackResultSchema,
		'POST',
		{},
		{ ...options, preserveErrors: true }
	);
}

export async function deleteVMSnapshot(
	rid: number,
	snapshotId: number,
	options: NodeAPIRequestOptions = {}
): Promise<APIResponse> {
	return await apiRequest(
		'/vm/' + rid + '/snapshots/' + snapshotId,
		APIResponseSchema,
		'DELETE',
		undefined,
		{ ...options, preserveErrors: true }
	);
}

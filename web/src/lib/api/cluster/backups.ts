import {
	BackupDatasetSchema,
	BackupEventSchema,
	BackupJobSchema,
	BackupPlanSchema,
	BackupTargetSchema,
	type BackupDataset,
	type BackupEvent,
	type BackupJob,
	type BackupPlan,
	type BackupTarget
} from '$lib/types/cluster/backups';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export type BackupTargetInput = {
	name: string;
	endpoint: string;
	description?: string;
	enabled: boolean;
};

export type BackupJobInput = {
	name: string;
	targetId: number;
	runnerNodeId: string;
	mode: 'dataset' | 'jails';
	sourceDataset?: string;
	jailRootDataset?: string;
	destinationDataset: string;
	cronExpr: string;
	force: boolean;
	withIntermediates: boolean;
	enabled: boolean;
};

export type BackupPullInput = {
	targetId: number;
	sourceDataset: string;
	destinationDataset: string;
	snapshot?: string;
	force: boolean;
	withIntermediates: boolean;
};

export async function listBackupTargets(): Promise<BackupTarget[]> {
	return await apiRequest('/cluster/backups/targets', z.array(BackupTargetSchema), 'GET');
}

export async function createBackupTarget(input: BackupTargetInput): Promise<APIResponse> {
	return await apiRequest('/cluster/backups/targets', APIResponseSchema, 'POST', input);
}

export async function updateBackupTarget(id: number, input: BackupTargetInput): Promise<APIResponse> {
	return await apiRequest(`/cluster/backups/targets/${id}`, APIResponseSchema, 'PUT', input);
}

export async function deleteBackupTarget(id: number): Promise<APIResponse> {
	return await apiRequest(`/cluster/backups/targets/${id}`, APIResponseSchema, 'DELETE');
}

export async function listBackupJobs(): Promise<BackupJob[]> {
	return await apiRequest('/cluster/backups/jobs', z.array(BackupJobSchema), 'GET');
}

export async function createBackupJob(input: BackupJobInput): Promise<APIResponse> {
	return await apiRequest('/cluster/backups/jobs', APIResponseSchema, 'POST', input);
}

export async function updateBackupJob(id: number, input: BackupJobInput): Promise<APIResponse> {
	return await apiRequest(`/cluster/backups/jobs/${id}`, APIResponseSchema, 'PUT', input);
}

export async function deleteBackupJob(id: number): Promise<APIResponse> {
	return await apiRequest(`/cluster/backups/jobs/${id}`, APIResponseSchema, 'DELETE');
}

export async function runBackupJob(id: number): Promise<APIResponse> {
	return await apiRequest(`/cluster/backups/jobs/${id}/run`, APIResponseSchema, 'POST', {});
}

export async function getBackupTargetDatasets(id: number, prefix?: string): Promise<BackupDataset[]> {
	const q = prefix ? `?prefix=${encodeURIComponent(prefix)}` : '';
	return await apiRequest(
		`/cluster/backups/targets/${id}/datasets${q}`,
		z.array(BackupDatasetSchema),
		'GET'
	);
}

export async function getBackupTargetStatus(id: number, limit: number = 50): Promise<BackupEvent[]> {
	return await apiRequest(
		`/cluster/backups/targets/${id}/status?limit=${limit}`,
		z.array(BackupEventSchema),
		'GET'
	);
}

export async function getBackupEvents(limit: number = 200, jobId?: number): Promise<BackupEvent[]> {
	const params = new URLSearchParams();
	params.set('limit', String(limit));
	if (jobId && jobId > 0) {
		params.set('jobId', String(jobId));
	}
	return await apiRequest(`/cluster/backups/events?${params.toString()}`, z.array(BackupEventSchema), 'GET');
}

export async function pullFromBackupTarget(input: BackupPullInput): Promise<BackupPlan> {
	return await apiRequest('/cluster/backups/pull', BackupPlanSchema, 'POST', input);
}

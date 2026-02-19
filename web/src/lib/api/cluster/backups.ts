import {
    BackupEventSchema,
    BackupJobSchema,
    BackupTargetSchema,
    SnapshotInfoSchema,
    type BackupEvent,
    type BackupJob,
    type BackupTarget,
    type SnapshotInfo
} from '$lib/types/cluster/backups';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export type BackupTargetInput = {
    name: string;
    sshHost: string;
    sshPort: number;
    sshKey?: string;
    backupRoot: string;
    description?: string;
    enabled: boolean;
};

export type BackupJobInput = {
    name: string;
    targetId: number;
    runnerNodeId: string;
    mode: 'dataset' | 'jail';
    sourceDataset?: string;
    jailRootDataset?: string;
    destSuffix?: string;
    pruneKeepLast: number;
    pruneTarget: boolean;
    cronExpr: string;
    enabled: boolean;
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

export async function validateBackupTarget(id: number): Promise<APIResponse> {
    return await apiRequest(`/cluster/backups/targets/${id}/validate`, APIResponseSchema, 'POST', {});
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

export async function getBackupEvents(limit: number = 200, jobId?: number): Promise<BackupEvent[]> {
    const params = new URLSearchParams();
    params.set('limit', String(limit));
    if (jobId && jobId > 0) {
        params.set('jobId', String(jobId));
    }
    return await apiRequest(`/cluster/backups/events?${params.toString()}`, z.array(BackupEventSchema), 'GET');
}

export async function listBackupJobSnapshots(jobId: number): Promise<SnapshotInfo[]> {
    return await apiRequest(`/cluster/backups/jobs/${jobId}/snapshots`, z.array(SnapshotInfoSchema), 'GET');
}

export async function restoreBackupJob(jobId: number, snapshot: string): Promise<APIResponse> {
    return await apiRequest(`/cluster/backups/jobs/${jobId}/restore`, APIResponseSchema, 'POST', { snapshot });
}

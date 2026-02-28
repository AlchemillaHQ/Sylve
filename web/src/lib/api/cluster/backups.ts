import {
    BackupJailMetadataInfoSchema,
    BackupVMMetadataInfoSchema,
    BackupEventSchema,
    BackupEventProgressSchema,
    BackupJobSchema,
    BackupTargetDatasetInfoSchema,
    BackupTargetSchema,
    SnapshotInfoSchema,
    type BackupJailMetadataInfo,
    type BackupVMMetadataInfo,
    type BackupEvent,
    type BackupEventProgress,
    type BackupJob,
    type BackupTargetDatasetInfo,
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
    createBackupRoot: boolean;
    description?: string;
    enabled: boolean;
};

export type BackupJobInput = {
    name: string;
    targetId: number;
    runnerNodeId: string;
    mode: 'dataset' | 'jail' | 'vm';
    sourceDataset?: string;
    jailRootDataset?: string;
    destSuffix?: string;
    pruneKeepLast: number;
    pruneTarget: boolean;
    stopBeforeBackup: boolean;
    cronExpr: string;
    enabled: boolean;
};

export type RestoreFromTargetInput = {
    remoteDataset: string;
    snapshot: string;
    destinationDataset: string;
    restoreNodeId?: string;
    restoreNetwork?: boolean;
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
    return await apiRequest(`/cluster/backups/targets/validate/${id}`, APIResponseSchema, 'POST', {});
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
    return await apiRequest(`/cluster/backups/jobs/run/${id}`, APIResponseSchema, 'POST', {});
}

export async function getBackupEvents(
    limit: number = 200,
    jobId?: number,
    nodeId?: string
): Promise<BackupEvent[]> {
    const params = new URLSearchParams();
    params.set('limit', String(limit));
    if (jobId && jobId > 0) {
        params.set('jobId', String(jobId));
    }
    if (nodeId && nodeId.trim() !== '') {
        params.set('nodeId', nodeId.trim());
    }
    return await apiRequest(`/cluster/backups/events?${params.toString()}`, z.array(BackupEventSchema), 'GET');
}

export async function getBackupEvent(id: number, nodeId?: string): Promise<BackupEvent> {
    const params = new URLSearchParams();
    if (nodeId && nodeId.trim() !== '') {
        params.set('nodeId', nodeId.trim());
    }
    const query = params.toString();
    return await apiRequest(`/cluster/backups/events/${id}${query ? `?${query}` : ''}`, BackupEventSchema, 'GET');
}

export async function getBackupEventProgress(id: number, nodeId?: string): Promise<BackupEventProgress> {
    const params = new URLSearchParams();
    if (nodeId && nodeId.trim() !== '') {
        params.set('nodeId', nodeId.trim());
    }
    const query = params.toString();
    return await apiRequest(`/cluster/backups/events/${id}/progress${query ? `?${query}` : ''}`, BackupEventProgressSchema, 'GET');
}

export async function listBackupJobSnapshots(jobId: number): Promise<SnapshotInfo[]> {
    return await apiRequest(`/cluster/backups/jobs/${jobId}/snapshots`, z.array(SnapshotInfoSchema), 'GET');
}

export async function restoreBackupJob(jobId: number, snapshot: string): Promise<APIResponse> {
    return await apiRequest(`/cluster/backups/jobs/${jobId}/restore`, APIResponseSchema, 'POST', { snapshot });
}

export async function listBackupTargetDatasets(targetId: number): Promise<BackupTargetDatasetInfo[]> {
    return await apiRequest(`/cluster/backups/targets/${targetId}/datasets`, z.array(BackupTargetDatasetInfoSchema), 'GET');
}

export async function listBackupTargetDatasetSnapshots(targetId: number, dataset: string): Promise<SnapshotInfo[]> {
    const params = new URLSearchParams();
    params.set('dataset', dataset);
    return await apiRequest(`/cluster/backups/targets/${targetId}/datasets/snapshots?${params.toString()}`, z.array(SnapshotInfoSchema), 'GET');
}

export async function getBackupTargetJailMetadata(targetId: number, dataset: string): Promise<BackupJailMetadataInfo | null> {
    const params = new URLSearchParams();
    params.set('dataset', dataset);
    return await apiRequest(`/cluster/backups/targets/${targetId}/datasets/jail-metadata?${params.toString()}`, BackupJailMetadataInfoSchema.nullable(), 'GET');
}

export async function getBackupTargetVMMetadata(targetId: number, dataset: string): Promise<BackupVMMetadataInfo | null> {
    const params = new URLSearchParams();
    params.set('dataset', dataset);
    return await apiRequest(`/cluster/backups/targets/${targetId}/datasets/vm-metadata?${params.toString()}`, BackupVMMetadataInfoSchema.nullable(), 'GET');
}

export async function restoreBackupFromTarget(targetId: number, input: RestoreFromTargetInput): Promise<APIResponse> {
    return await apiRequest(`/cluster/backups/targets/${targetId}/restore`, APIResponseSchema, 'POST', input);
}

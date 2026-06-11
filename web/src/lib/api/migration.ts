import type { APIResponse } from '$lib/types/common';
import {
    ValidateResultSchema,
    MigrationTaskResponseSchema,
    type ValidateResult,
    type MigrationTaskResponse
} from '$lib/types/migration';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function migrateVM(
    rid: number,
    targetNodeUuid: string,
    hostname?: string
): Promise<MigrationTaskResponse | APIResponse> {
    return await apiRequest(
        `/vm/migrate/${rid}`,
        MigrationTaskResponseSchema,
        'POST',
        { targetNodeUuid },
        { hostname }
    );
}

export async function migrateJail(
    ctId: number,
    targetNodeUuid: string,
    hostname?: string
): Promise<MigrationTaskResponse | APIResponse> {
    return await apiRequest(
        `/jail/migrate/${ctId}`,
        MigrationTaskResponseSchema,
        'POST',
        { targetNodeUuid },
        { hostname }
    );
}

export async function validateMigration(
    guestType: 'vm' | 'jail',
    guestId: number,
    targetNodeUuid: string,
    hostname?: string
): Promise<ValidateResult | APIResponse> {
    const params = new URLSearchParams({ guestType, guestId: String(guestId), targetNodeUuid });
    return await apiRequest(
        `/tasks/migration/validate?${params.toString()}`,
        ValidateResultSchema,
        'GET',
        undefined,
        { hostname }
    );
}

export async function cancelMigration(
    taskId: number,
    hostname?: string
): Promise<APIResponse> {
    return await apiRequest(
        `/tasks/migration/cancel/${taskId}`,
        z.any(),
        'POST',
        undefined,
        { hostname, raw: true }
    );
}

import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import type { BootstrapEntry, BootstrapRequest } from '$lib/types/jail/bootstrap';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

const BootstrapEntrySchema = z.object({
    pool: z.string(),
    name: z.string(),
    label: z.string(),
    dataset: z.string(),
    mountPoint: z.string(),
    major: z.number(),
    minor: z.number(),
    type: z.string(),
    exists: z.boolean(),
    status: z.string(),
    phase: z.string(),
    error: z.string()
});

export async function getBootstraps(pool: string): Promise<BootstrapEntry[]> {
    return await apiRequest(`/jail/bootstraps?pool=${encodeURIComponent(pool)}`, z.array(BootstrapEntrySchema), 'GET');
}

export async function createBootstrap(req: BootstrapRequest): Promise<APIResponse> {
    return await apiRequest('/jail/bootstrap', APIResponseSchema, 'POST', req);
}

export async function deleteBootstrap(pool: string, name: string): Promise<APIResponse> {
    return await apiRequest(
        `/jail/bootstrap?pool=${encodeURIComponent(pool)}&name=${encodeURIComponent(name)}`,
        APIResponseSchema,
        'DELETE'
    );
}

import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';

export async function setTunable(name: string, value: string): Promise<APIResponse> {
    return apiRequest('/system/tunables', APIResponseSchema, 'PUT', { name, value });
}

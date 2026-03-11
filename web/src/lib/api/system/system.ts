import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export const BasicHealthSchema = z.object({
	hostname: z.string().optional(),
	initialized: z.boolean().optional(),
	restarted: z.boolean().optional()
});

export type BasicHealth = z.infer<typeof BasicHealthSchema>;

export async function rebootSystem(): Promise<APIResponse> {
	return apiRequest('/basic/system/reboot', APIResponseSchema, 'PUT');
}

export async function getBasicHealth(): Promise<BasicHealth> {
	return await apiRequest('/health/basic', BasicHealthSchema, 'GET');
}

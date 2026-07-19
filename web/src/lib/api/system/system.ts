import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';
import { storage } from '$lib';

export const BasicHealthSchema = z.object({
	hostname: z.string().optional(),
	initialized: z.boolean(),
	restarted: z.boolean(),
	sylveVersion: z.string().optional()
});

export type BasicHealth = z.infer<typeof BasicHealthSchema>;

const BasicHealthResponseSchema = APIResponseSchema.extend({
	data: BasicHealthSchema
});

export async function rebootSystem(): Promise<APIResponse> {
	return apiRequest('/basic/system/reboot', APIResponseSchema, 'PUT');
}

export async function getBasicHealth(): Promise<BasicHealth | APIResponse> {
	return await apiRequest('/health/basic', BasicHealthSchema, 'GET');
}

export async function probeBasicHealth(): Promise<BasicHealth | null> {
	try {
		const response = await fetch('/api/health/basic', {
			headers: {
				Authorization: `Bearer ${storage.token}`
			}
		});
		if (!response.ok) {
			return null;
		}

		const parsed = BasicHealthResponseSchema.safeParse(await response.json());
		if (!parsed.success || parsed.data.status !== 'success') {
			return null;
		}

		return parsed.data.data;
	} catch {
		return null;
	}
}

import type { APIResponse } from '$lib/types/common';
import {
	apiRequest,
	isAPIResponse,
	removeCache,
	type NodeAPIRequestOptions
} from '$lib/utils/http';
import { z } from 'zod/v4';

const authCacheKeys = ['users', 'users_local', 'users_pam', 'groups'] as const;

export async function authMutation<T extends z.ZodType>(
	endpoint: string,
	schema: T,
	method: 'POST' | 'PUT' | 'DELETE',
	body: unknown,
	options: NodeAPIRequestOptions = {}
): Promise<z.infer<T> | APIResponse> {
	const hostname = options.hostname;
	const result = await apiRequest(endpoint, schema, method, body, { ...options, hostname });
	if (!isAPIResponse(result)) {
		await Promise.all(authCacheKeys.map((key) => removeCache(key, hostname)));
	}
	return result;
}

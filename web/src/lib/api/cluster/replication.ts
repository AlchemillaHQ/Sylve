import {
	ReplicationEventProgressSchema,
	ReplicationEventSchema,
	ReplicationPolicySchema,
	type ReplicationFailbackMode,
	type ReplicationGuestType,
	type ReplicationPolicy,
	type ReplicationSourceMode
} from '$lib/types/cluster/replication';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export type ReplicationPolicyTargetInput = {
	nodeId: string;
	weight: number;
};

export type ReplicationPolicyInput = {
	name: string;
	guestType: ReplicationGuestType;
	guestId: number;
	enabled: boolean;
	cronExpr: string;
	targets: ReplicationPolicyTargetInput[];
	failbackMode: ReplicationFailbackMode;
	sourceMode: ReplicationSourceMode;
	sourceNodeId?: string;
};

export async function listReplicationPolicies(): Promise<ReplicationPolicy[]> {
	return await apiRequest('/cluster/replication/policies', z.array(ReplicationPolicySchema), 'GET');
}

export async function createReplicationPolicy(input: ReplicationPolicyInput): Promise<APIResponse> {
	return await apiRequest('/cluster/replication/policies', APIResponseSchema, 'POST', input);
}

export async function updateReplicationPolicy(
	id: number,
	input: ReplicationPolicyInput
): Promise<APIResponse> {
	return await apiRequest(`/cluster/replication/policies/${id}`, APIResponseSchema, 'PUT', input);
}

export async function deleteReplicationPolicy(id: number): Promise<APIResponse> {
	return await apiRequest(`/cluster/replication/policies/${id}`, APIResponseSchema, 'DELETE');
}

export async function runReplicationPolicy(id: number): Promise<APIResponse> {
	return await apiRequest(`/cluster/replication/policies/${id}/run`, APIResponseSchema, 'POST', {});
}

export async function listReplicationEvents(
	limit: number = 200,
	policyId?: number
): Promise<z.infer<typeof ReplicationEventSchema>[]> {
	const params = new URLSearchParams();
	params.set('limit', String(limit));
	if (policyId && policyId > 0) {
		params.set('policyId', String(policyId));
	}

	return await apiRequest(
		`/cluster/replication/events?${params.toString()}`,
		z.array(ReplicationEventSchema),
		'GET'
	);
}

export async function getReplicationEvent(
	id: number
): Promise<z.infer<typeof ReplicationEventSchema>> {
	return await apiRequest(`/cluster/replication/events/${id}`, ReplicationEventSchema, 'GET');
}

export async function getReplicationEventProgress(
	id: number
): Promise<z.infer<typeof ReplicationEventProgressSchema>> {
	return await apiRequest(
		`/cluster/replication/events/${id}/progress`,
		ReplicationEventProgressSchema,
		'GET'
	);
}

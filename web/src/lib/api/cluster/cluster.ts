import {
	ClusterDetailsSchema,
	ClusterNodeSchema,
	NodeResourceSchema,
	type ClusterDetails,
	type ClusterNode,
	type NodeResource
} from '$lib/types/cluster/cluster';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { reload } from '$lib/stores/api.svelte';
import {
	apiRequest,
	apiRequestData,
	apiRequestResult,
	removeCache,
	type NodeAPIDataRequestOptions
} from '$lib/utils/http';
import { z } from 'zod/v4';

const JoinKeySchema = z.object({
	key: z.string().min(1)
});

const clusterLifecycleCacheKeys = [
	'cluster-info',
	'cluster-details',
	'cluster-nodes',
	'cluster-notes',
	'backup-targets',
	'replication-policies',
	'replication-events'
];

export async function refreshClusterAfterLifecycleChange(): Promise<void> {
	await Promise.all(clusterLifecycleCacheKeys.map((key) => removeCache(key)));
	reload.clusterDetails = true;
}

export async function getDetails(): Promise<ClusterDetails | APIResponse> {
	return await apiRequestResult('/cluster', ClusterDetailsSchema, 'GET');
}

export async function getDetailsData(options?: NodeAPIDataRequestOptions): Promise<ClusterDetails> {
	return await apiRequestData('/cluster', ClusterDetailsSchema, 'GET', undefined, options);
}

export async function getJoinKey(): Promise<z.infer<typeof JoinKeySchema> | APIResponse> {
	return await apiRequestResult('/cluster/join-key', JoinKeySchema, 'GET');
}

export async function createCluster(ip: string): Promise<APIResponse> {
	return await apiRequest(
		'/cluster',
		APIResponseSchema,
		'POST',
		{
			ip: ip
		},
		{ raw: true }
	);
}

export async function joinCluster(
	nodeId: string,
	nodeIp: string,
	leaderIp: string,
	clusterKey: string
): Promise<APIResponse> {
	return await apiRequest('/cluster/join', APIResponseSchema, 'POST', {
		nodeId: nodeId,
		nodeIp: nodeIp,
		leaderIp: leaderIp,
		clusterKey: clusterKey
	});
}

export async function resetCluster(): Promise<APIResponse> {
	return await apiRequest('/cluster/reset-node', APIResponseSchema, 'DELETE');
}

export async function removePeer(nodeId: string): Promise<APIResponse> {
	return await apiRequest(
		'/cluster/remove-node',
		APIResponseSchema,
		'POST',
		{ nodeId },
		{ raw: true }
	);
}

export async function getNodes(signal?: AbortSignal): Promise<ClusterNode[]> {
	return await apiRequestData('/cluster/nodes', z.array(ClusterNodeSchema), 'GET', undefined, {
		signal
	});
}

export async function getNodesResult(signal?: AbortSignal): Promise<ClusterNode[] | APIResponse> {
	return await apiRequestResult('/cluster/nodes', z.array(ClusterNodeSchema), 'GET', undefined, {
		signal
	});
}

export async function getClusterResources(signal?: AbortSignal): Promise<NodeResource[]> {
	return await apiRequestData('/cluster/resources', z.array(NodeResourceSchema), 'GET', undefined, {
		signal
	});
}

export async function getClusterResourcesResult(
	signal?: AbortSignal
): Promise<NodeResource[] | APIResponse> {
	return await apiRequestResult(
		'/cluster/resources',
		z.array(NodeResourceSchema),
		'GET',
		undefined,
		{
			signal
		}
	);
}

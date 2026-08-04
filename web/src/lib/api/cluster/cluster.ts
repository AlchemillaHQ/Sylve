import {
    ClusterDetailsSchema,
    ClusterNodeSchema,
    NodeResourceSchema,
    type ClusterDetails,
    type ClusterNode,
    type NodeResource
} from '$lib/types/cluster/cluster';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getDetails(): Promise<ClusterDetails | APIResponse> {
    return await apiRequest('/cluster', ClusterDetailsSchema, 'GET');
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

export async function getNodes(signal?: AbortSignal): Promise<ClusterNode[]> {
    return await apiRequest('/cluster/nodes', z.array(ClusterNodeSchema), 'GET', undefined, {
        signal
    });
}

export async function getNodesResult(signal?: AbortSignal): Promise<ClusterNode[] | APIResponse> {
    return await apiRequest('/cluster/nodes', z.array(ClusterNodeSchema), 'GET', undefined, {
        preserveErrors: true,
        signal
    });
}

export async function getClusterResources(signal?: AbortSignal): Promise<NodeResource[]> {
    return await apiRequest('/cluster/resources', z.array(NodeResourceSchema), 'GET', undefined, {
        signal
    });
}

export async function getClusterResourcesResult(
    signal?: AbortSignal
): Promise<NodeResource[] | APIResponse> {
    return await apiRequest('/cluster/resources', z.array(NodeResourceSchema), 'GET', undefined, {
        preserveErrors: true,
        signal
    });
}

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

export async function getDetails(): Promise<ClusterDetails> {
	return await apiRequest('/cluster', ClusterDetailsSchema, 'GET');
}

export async function createCluster(ip: string, port: number): Promise<APIResponse> {
	return await apiRequest('/cluster', APIResponseSchema, 'POST', {
		ip: ip,
		port: port
	});
}

export async function joinCluster(
	nodeId: string,
	nodeIp: string,
	nodePort: number,
	leaderApi: string,
	clusterKey: string
): Promise<APIResponse> {
	return await apiRequest('/cluster/join', APIResponseSchema, 'POST', {
		nodeId: nodeId,
		nodeIp: nodeIp,
		nodePort: nodePort,
		leaderApi: leaderApi,
		clusterKey: clusterKey
	});
}

export async function resetCluster(): Promise<APIResponse> {
	return await apiRequest('/cluster/reset-node', APIResponseSchema, 'DELETE');
}

export async function getNodes(): Promise<ClusterNode[]> {
	return await apiRequest('/cluster/nodes', z.array(ClusterNodeSchema), 'GET');
}

export async function getClusterResources(): Promise<NodeResource[]> {
	return await apiRequest('/cluster/resources', z.array(NodeResourceSchema), 'GET');
}

interface TreeNode {
	id: string;
	children?: TreeNode[];
}

export function collectIds(nodes: TreeNode[]): string[] {
	const ids: string[] = [];
	for (const node of nodes) {
		ids.push(node.id);
		if (node.children && node.children.length > 0) {
			ids.push(...collectIds(node.children));
		}
	}
	return ids;
}


export function saveOpenIds(ids: Set<string>) {
	localStorage.setItem('clusterIds', JSON.stringify(Array.from(ids)));
}

export function loadClusterIds(): Set<string> {
	try {
		const json = localStorage.getItem('clusterIds');
		if (json) {
			return new Set(JSON.parse(json));
		}
	} catch (error) {
		console.error('Failed to load clusterIds from localStorage:', error);
		localStorage.removeItem('clusterIds');
	}
	return new Set();
}
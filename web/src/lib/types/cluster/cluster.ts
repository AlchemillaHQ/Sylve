import { z } from 'zod/v4';
import { SimpleJailSchema, SimpleJailTemplateSchema } from '../jail/jail';
import { SimpleVmSchema, SimpleVmTemplateSchema } from '../vm/vm';

export const ClusterSchema = z.object({
	id: z.number(),
	enabled: z.boolean(),
	raftBootstrap: z.boolean().nullable(),
	raftIP: z.string(),
	raftPort: z.number().min(0).max(65535).optional()
});

export const RaftNodeSchema = z.object({
	id: z.string(),
	address: z.string(),
	suffrage: z.string(),
	isLeader: z.boolean(),
	guestIDs: z.union([z.array(z.number()), z.null()]).default([])
});

export const ClusterDetailsSchema = z.object({
	cluster: ClusterSchema,
	nodeId: z.string(),
	nodes: z.array(RaftNodeSchema).default([]),
	leaderId: z.string().optional(),
	leaderAddress: z.string().optional(),
	partial: z.boolean()
});

export const ClusterJoinStatusSchema = z.object({
	nodeId: z.string(),
	nodeIp: z.string().optional(),
	leaderIp: z.string().optional(),
	leaderId: z.string().optional(),
	leaderAddress: z.string().optional(),
	phase: z.string(),
	suffrage: z.string().optional(),
	raftState: z.string().optional(),
	appliedIndex: z.number().nonnegative(),
	targetIndex: z.number().nonnegative().optional(),
	attempts: z.number().int().nonnegative(),
	retrying: z.boolean(),
	lastError: z.string().optional()
});

export const ClusterLeaveStatusSchema = z.object({
	enabled: z.boolean(),
	leaveId: z.string(),
	phase: z.string(),
	leaderIp: z.string(),
	lastError: z.string(),
	attempts: z.number().int().nonnegative(),
	localNodeId: z.string()
});

export const ClusterNodeSchema = z.object({
	id: z.number(),
	nodeUUID: z.string(),
	status: z.string(),
	hostname: z.string(),
	api: z.string(),
	cpu: z.number(),
	cpuUsage: z.number(),
	memory: z.number(),
	memoryUsage: z.number(),
	disk: z.number(),
	diskUsage: z.number(),
	createdAt: z.string(),
	updatedAt: z.string(),
	guestIDs: z.union([z.array(z.number()), z.null()]).default([])
});

export const NodeResourceSchema = z.object({
	nodeUUID: z.string(),
	hostname: z.string(),
	jails: z.array(SimpleJailSchema).nullable().default([]),
	jailTemplates: z.array(SimpleJailTemplateSchema).nullable().default([]),
	vms: z.array(SimpleVmSchema).nullable().default([]),
	vmTemplates: z.array(SimpleVmTemplateSchema).nullable().default([])
});

export const PeerRemovalDependencySchema = z.object({
	kind: z.string(),
	id: z.string(),
	name: z.string().optional(),
	role: z.string().optional(),
	state: z.string().optional()
});

export const PeerRemovalConflictSchema = z.object({
	nodeId: z.string(),
	dependencies: z.array(PeerRemovalDependencySchema).default([])
});

export type Cluster = z.infer<typeof ClusterSchema>;
export type RaftNode = z.infer<typeof RaftNodeSchema>;
export type ClusterDetails = z.infer<typeof ClusterDetailsSchema>;
export type ClusterJoinStatus = z.infer<typeof ClusterJoinStatusSchema>;
export type ClusterLeaveStatus = z.infer<typeof ClusterLeaveStatusSchema>;
export type ClusterNode = z.infer<typeof ClusterNodeSchema>;
export type NodeResource = z.infer<typeof NodeResourceSchema>;
export type PeerRemovalDependency = z.infer<typeof PeerRemovalDependencySchema>;
export type PeerRemovalConflict = z.infer<typeof PeerRemovalConflictSchema>;

import { z } from 'zod/v4';
import { SimpleJailSchema } from '../jail/jail';
import { SimpleVmSchema } from '../vm/vm';

export const ClusterSchema = z.object({
    id: z.number(),
    enabled: z.boolean(),
    key: z.string(),
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
    vms: z.array(SimpleVmSchema).nullable().default([])
});

export type Cluster = z.infer<typeof ClusterSchema>;
export type RaftNode = z.infer<typeof RaftNodeSchema>;
export type ClusterDetails = z.infer<typeof ClusterDetailsSchema>;
export type ClusterNode = z.infer<typeof ClusterNodeSchema>;
export type NodeResource = z.infer<typeof NodeResourceSchema>;

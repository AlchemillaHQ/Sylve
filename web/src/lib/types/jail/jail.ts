import { z } from 'zod/v4';
import { NetworkObjectSchema } from '../network/object';

export interface CreateData {
	name: string;
	id: number;
	node: string;
	description: string;
	storage: {
		dataset: string;
		base: string;
	};
	network: {
		switch: string;
		mac: number;
		inheritIPv4: boolean;
		inheritIPv6: boolean;
		ipv4: number;
		ipv4Gateway: number;
		ipv6: number;
		ipv6Gateway: number;
		dhcp: boolean;
		slaac: boolean;
	};
	hardware: {
		cpuCores: number;
		ram: number;
		startAtBoot: boolean;
		resourceLimits: boolean;
		bootOrder: number;
	};
}

export const SimpleJailSchema = z.object({
	id: z.number().int(),
	name: z.string(),
	ctId: z.number().int(),
	state: z.enum(['ACTIVE', 'INACTIVE', 'UNKNOWN']).optional()
});

export const NetworkSchema = z.object({
	id: z.number().int(),
	switchId: z.number().int(),
	switchType: z.enum(['standard', 'manual']),
	macId: z.number().int().nullable(),
    macObj: NetworkObjectSchema.optional(),
	ipv4Id: z.number().int().nullable(),
	ipv4GwId: z.number().int().nullable(),
	ipv6Id: z.number().int().nullable(),
	ipv6GwId: z.number().int().nullable(),
	ctId: z.number().int(),
	dhcp: z.boolean().nullable().default(false),
	slaac: z.boolean().nullable().default(false)
});

export const JailSchema = SimpleJailSchema.extend({
	description: z.string().nullable(),
	dataset: z.string(),
	base: z.string(),
	startAtBoot: z.boolean(),
	startOrder: z.number().int(),
	inheritIPv4: z.boolean(),
	inheritIPv6: z.boolean(),
	networks: z.array(NetworkSchema).optional().default([]),
	createdAt: z.string(),
	resourceLimits: z.boolean().optional().default(false),
	cores: z.number().int(),
	memory: z.number().int(),
	updatedAt: z.string(),
	startedAt: z.string().nullable(),
	stoppedAt: z.string().nullable()
});

export const JailStateSchema = z.object({
	ctId: z.number().int(),
	state: z.enum(['ACTIVE', 'INACTIVE', 'UNKNOWN']),
	pcpu: z.number(),
	memory: z.number()
});

export const JailLogsSchema = z.object({
	logs: z.string()
});

export const JailStatSchema = z.object({
	id: z.number().int(),
	ctId: z.number().int(),
	cpuUsage: z.number(),
	memoryUsage: z.number(),
	createdAt: z.string()
});

export type SimpleJail = z.infer<typeof SimpleJailSchema>;
export type Jail = z.infer<typeof JailSchema>;
export type JailState = z.infer<typeof JailStateSchema>;
export type JailLogs = z.infer<typeof JailLogsSchema>;
export type JailStat = z.infer<typeof JailStatSchema>;

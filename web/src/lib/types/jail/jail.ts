import { z } from 'zod/v4';
import { NetworkObjectSchema } from '../network/object';

export interface CreateData {
	name: string;
	hostname: string;
	id: number;
	node: string;
	description: string;
	storage: {
		pool: string;
		base: string;
		fstab: string;
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
		devfsRuleset: string;
	};
	advanced: {
		jailType: 'linux' | 'freebsd';
		additionalOptions: string;
		cleanEnvironment: boolean;
		execScripts: Record<ExecPhaseKey, ExecPhaseState>;
		allowedOptions: string[];
		metadata: {
			env: string;
			meta: string;
		};
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
	jid: z.number().int(),
	name: z.string(),
	switchId: z.number().int(),
	switchType: z.enum(['standard', 'manual']),
	macId: z.number().int().nullable(),
	macObj: NetworkObjectSchema.optional(),
	ipv4Id: z.number().int().nullable(),
	ipv4GwId: z.number().int().nullable(),
	ipv6Id: z.number().int().nullable(),
	ipv6GwId: z.number().int().nullable(),
	dhcp: z.boolean().nullable().default(false),
	slaac: z.boolean().nullable().default(false),
	defaultGateway: z.boolean().default(false)
});

export const JailSchema = SimpleJailSchema.extend({
	description: z.string().nullable(),
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
	stoppedAt: z.string().nullable(),
	type: z.enum(['freebsd', 'linux']),
	fstab: z.string(),
	devfsRuleset: z.string()
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
	jid: z.number().int(),
	cpuUsage: z.number(),
	memoryUsage: z.number(),
	createdAt: z.string()
});

export const ExecPhaseDefs = [
	{
		key: 'prestart',
		label: 'Pre-start (exec.prestart)',
		description: 'Runs on the host before the jail starts'
	},
	{
		key: 'start',
		label: 'Start (exec.start)',
		description: 'Runs inside the jail to start it, Often /bin/sh /etc/rc'
	},
	{
		key: 'poststart',
		label: 'Post-start (exec.poststart)',
		description: 'Runs inside the jail after it has started'
	},
	{
		key: 'prestop',
		label: 'Pre-stop (exec.prestop)',
		description: 'Runs inside the jail before it is stopped'
	},
	{
		key: 'stop',
		label: 'Stop (exec.stop)',
		description: 'Runs inside the jail to stop it, Often /bin/sh /etc/rc.shutdown'
	},
	{
		key: 'poststop',
		label: 'Post-stop (exec.poststop)',
		description: 'Runs on the host after the jail has stopped'
	}
] as const;

export type ExecPhaseKey = (typeof ExecPhaseDefs)[number]['key'];
export interface ExecPhaseState {
	enabled: boolean;
	script: string;
}

export type SimpleJail = z.infer<typeof SimpleJailSchema>;
export type Jail = z.infer<typeof JailSchema>;
export type JailNetwork = z.infer<typeof NetworkSchema>;
export type JailState = z.infer<typeof JailStateSchema>;
export type JailLogs = z.infer<typeof JailLogsSchema>;
export type JailStat = z.infer<typeof JailStatSchema>;

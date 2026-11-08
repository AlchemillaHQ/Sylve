/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { z } from 'zod/v4';
import { GFSStepSchema, StatsHistoryStateSchema } from '../common';

export const list = <T extends z.ZodType>(schema: T) =>
	z
		.array(schema)
		.nullish()
		.transform((value) => value ?? []);

export const ContainerNetworkSchema = z.object({
	id: z.number().int(),
	name: z.string(),
	switchId: z.number().int().nullable(),
	switchType: z.string(),
	bridge: z.string(),
	subnet: z.string(),
	gateway: z.string(),
	rangeStart: z.string(),
	rangeEnd: z.string()
});

export const ContainerEnvSchema = z.object({ name: z.string(), value: z.string() });

export const ContainerPortSchema = z.object({
	protocol: z.enum(['tcp', 'udp']),
	hostIp: z.string().optional(),
	hostPort: z.number().int(),
	containerPort: z.number().int()
});

export const ContainerMountSchema = z.object({
	type: z.enum(['volume', 'dataset', 'path']),
	source: z.string(),
	target: z.string(),
	readOnly: z.boolean(),
	guid: z.string().optional(),
	owner: z.string().optional(),
	mode: z.string().optional()
});

export const ContainerRestartSchema = z.enum(['no', 'on-failure', 'always', 'unless-stopped']);

export const ContainerSchema = z.object({
	ocId: z.number().int(),
	name: z.string(),
	title: z.string().catch(''),
	description: z.string(),
	pool: z.string(),
	image: z.string(),
	os: z.string(),
	command: list(z.string()),
	entrypoint: list(z.string()),
	workDir: z.string(),
	user: z.string(),
	hostname: z.string(),
	env: list(ContainerEnvSchema),
	network: ContainerNetworkSchema.nullish(),
	ip: z.string(),
	ports: list(ContainerPortSchema),
	mounts: list(ContainerMountSchema),
	cores: z.number().int(),
	memory: z.number(),
	restart: ContainerRestartSchema.catch('no'),
	restartRetries: z.number().int(),
	stopTimeout: z.number().int(),
	annotations: z
		.record(z.string(), z.string())
		.nullish()
		.transform((value) => value ?? {}),
	extraHosts: list(z.string()),
	shmSize: z.string().catch(''),
	startAtBoot: z.boolean(),
	startOrder: z.number().int(),
	webPort: z.number().int().catch(0),
	webHttps: z.boolean().catch(false),
	catalogApp: z.string().catch(''),
	stack: z.string().catch(''),
	stackTitle: z.string().catch(''),
	service: z.string().catch(''),
	runtimeId: z.string(),
	creationPending: z.boolean()
});

export const ContainerListItemSchema = ContainerSchema.extend({ state: z.string() });

export const SimpleContainerSchema = z.object({
	ocId: z.number().int(),
	name: z.string(),
	title: z.string().catch(''),
	stack: z.string().catch(''),
	stackTitle: z.string().catch(''),
	state: z.string()
});

// containerTitle is the name a container is shown under: its title, or else
// its name, which is also its hostname and its name on its network.
export const containerTitle = (container: { name: string; title: string }) =>
	container.title || container.name;

export const ContainerRuntimeSchema = z.object({
	state: z.string(),
	startedAt: z.string(),
	finishedAt: z.string(),
	ips: list(z.string()),
	macs: list(z.string())
});

export const ContainerDetailSchema = ContainerSchema.extend({
	runtime: ContainerRuntimeSchema.nullable(),
	cpuUsage: z.number(),
	memoryUsage: z.number()
});

export const ContainerTaskSchema = z.object({
	taskId: z.number().int().positive(),
	ocId: z.number().int().positive()
});

export const ContainerStatSchema = z.object({
	cpuUsage: z.number(),
	memoryUsage: z.number(),
	createdAt: z.string()
});

export const ContainerStatsBootstrapSchema = z.object({
	points: z.array(ContainerStatSchema),
	resolvedStep: GFSStepSchema.nullable(),
	historyState: StatsHistoryStateSchema
});

export const ContainerImageSchema = z.object({
	id: z.string(),
	names: list(z.string()),
	previous: z.string().catch(''),
	size: z.number(),
	created: z.string()
});

export const ContainerImageInfoSchema = z.object({
	id: z.string(),
	digest: z.string(),
	size: z.number(),
	title: z.string(),
	description: z.string(),
	version: z.string(),
	url: z.string(),
	exposedPorts: list(z.string()),
	volumes: list(z.string())
});

export const UsedPortSchema = z.object({
	protocol: z.string(),
	port: z.number().int(),
	address: z.string(),
	by: z.string()
});

export const RegistryCredentialSchema = z.object({
	id: z.number().int(),
	registry: z.string(),
	username: z.string(),
	updatedAt: z.string()
});

export type ContainerNetwork = z.infer<typeof ContainerNetworkSchema>;
export type ContainerEnv = z.infer<typeof ContainerEnvSchema>;
export type ContainerPort = z.infer<typeof ContainerPortSchema>;
export type ContainerMount = z.infer<typeof ContainerMountSchema>;
export type ContainerRestart = z.infer<typeof ContainerRestartSchema>;
export type Container = z.infer<typeof ContainerSchema>;
export type ContainerListItem = z.infer<typeof ContainerListItemSchema>;
export type SimpleContainer = z.infer<typeof SimpleContainerSchema>;
export type ContainerDetail = z.infer<typeof ContainerDetailSchema>;
export type ContainerTask = z.infer<typeof ContainerTaskSchema>;
export type ContainerStat = z.infer<typeof ContainerStatSchema>;
export type ContainerStatsBootstrap = z.infer<typeof ContainerStatsBootstrapSchema>;
export type ContainerImage = z.infer<typeof ContainerImageSchema>;
export type ContainerImageInfo = z.infer<typeof ContainerImageInfoSchema>;
export type RegistryCredential = z.infer<typeof RegistryCredentialSchema>;
export type UsedPort = z.infer<typeof UsedPortSchema>;
export type ContainerAction = 'start' | 'stop' | 'restart' | 'kill';

export const restartLabels: Record<ContainerRestart, string> = {
	no: 'Never',
	'on-failure': 'On failure',
	always: 'Always',
	'unless-stopped': 'Unless stopped'
};

export const mountTypeLabels: Record<ContainerMount['type'], string> = {
	volume: 'Volume',
	dataset: 'ZFS Dataset',
	path: 'Host Directory'
};

export interface ContainerSpec {
	name?: string;
	image: string;
	os: '' | 'freebsd' | 'linux';
	command: string[];
	entrypoint: string[];
	workDir: string;
	user: string;
	hostname: string;
	env: ContainerEnv[];
	network: string;
	ip: string;
	ports: ContainerPort[];
	mounts: ContainerMount[];
	restart: ContainerRestart;
	restartRetries: number;
	stopTimeout: number;
	annotations: Record<string, string>;
	extraHosts: string[];
	shmSize: string;
	pullImage: boolean;
}

export interface ContainerCreateRequest extends ContainerSpec {
	ocId: number;
	name: string;
	description: string;
	pool: string;
	cores: number;
	memory: number;
	startAtBoot: boolean;
	startOrder: number;
	startAfterCreate: boolean;
	webPort: number;
	webHttps: boolean;
	catalogApp: string;
	stack?: string;
	title?: string;
	stackTitle?: string;
	service?: string;
	secrets?: Record<string, string>;
}

export interface ContainerOptions {
	title?: string;
	description?: string;
	cores?: number;
	memory?: number;
	startAtBoot?: boolean;
	startOrder?: number;
}

export interface ContainerNetworkRequest {
	name: string;
	switch?: string;
	subnet?: string;
	gateway?: string;
	rangeStart?: string;
	rangeEnd?: string;
}

export const emptyContainerSpec = (): ContainerSpec => ({
	image: '',
	os: '',
	command: [],
	entrypoint: [],
	workDir: '',
	user: '',
	hostname: '',
	env: [],
	network: 'default',
	ip: '',
	ports: [],
	mounts: [],
	restart: 'no',
	restartRetries: 0,
	stopTimeout: 10,
	annotations: {},
	extraHosts: [],
	shmSize: '',
	pullImage: false
});

export const containerSpecOf = (c: Container): ContainerSpec => ({
	name: c.name,
	image: c.image,
	os: c.os === 'freebsd' || c.os === 'linux' ? c.os : '',
	command: [...c.command],
	entrypoint: [...c.entrypoint],
	workDir: c.workDir,
	user: c.user,
	hostname: c.hostname,
	env: c.env.map((env) => ({ ...env })),
	network: c.network?.name ?? 'host',
	ip: c.ip,
	ports: c.ports.map((port) => ({ ...port })),
	mounts: c.mounts.map((mount) => ({ ...mount })),
	restart: c.restart,
	restartRetries: c.restartRetries,
	stopTimeout: c.stopTimeout,
	annotations: { ...c.annotations },
	extraHosts: [...c.extraHosts],
	shmSize: c.shmSize,
	pullImage: false
});

export const containerBusyTitle = 'Wait for the current container operation to finish';

export interface ContainerContext {
	readonly current: ContainerDetail;
	readonly pendingAction: string;
	readonly busy: boolean;
	refetch(): Promise<unknown>;
	update(spec: ContainerSpec): Promise<boolean>;
	setOptions(options: ContainerOptions): Promise<boolean>;
}

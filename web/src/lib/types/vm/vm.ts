import { z } from 'zod/v4';
import { NetworkObjectSchema } from '../network/object';

export interface CPUPin {
	socket: number;
	cores: number[];
}

export interface CreateData {
	name: string;
	node: string;
	id: number;
	description: string;
	storage: {
		type: string;
		pool: string;
		size: number;
		emulation: string;
		iso: string;
	};
	network: {
		switch: string;
		mac: string;
		emulation: string;
	};
	hardware: {
		sockets: number;
		cores: number;
		threads: number;
		memory: number;
		passthroughIds: number[];
		pinnedCPUs: CPUPin[];
		isPinningOpen: boolean;
	};
	advanced: {
		serial: boolean;
		vncPort: number;
		vncPassword: string;
		vncWait: boolean;
		vncResolution: string;
		startAtBoot: boolean;
		bootOrder: number;
		tpmEmulation: boolean;
		timeOffset: 'utc' | 'localtime';
	};
}

export type VMStorageType = 'raw' | 'zvol' | 'image';
export type VMStorageEmulationType = 'virtio-blk' | 'ahci-hd' | 'ahci-cd' | 'nvme';

export const VMStorageDatasetSchema = z.object({
	id: z.number().int(),
	pool: z.string(),
	name: z.string(),
	guid: z.string(),
	vmId: z.number().int()
});

export const VMStorageSchema = z.object({
	id: z.number().int(),
	vmId: z.number().int().optional(),
	name: z.string().optional(),
	type: z.enum(['raw', 'zvol', 'image']),
	uuid: z.string().optional(),
	datasetId: z.number().int().nullable(),
	dataset: VMStorageDatasetSchema.nullable(),
	size: z.number().int(),
	emulation: z.enum(['virtio-blk', 'ahci-hd', 'ahci-cd', 'nvme']),
	recordSize: z.number().int().optional(),
	volBlockSize: z.number().int().optional(),
	bootOrder: z.number().int().optional()
});

export const VMNetworkSchema = z.object({
	id: z.number().int(),
	mac: z.string(),
	macId: z.number().int().optional(),
	macObj: NetworkObjectSchema.optional(),
	switchId: z.number().int(),
	switchType: z.enum(['standard', 'manual']),
	emulation: z.string(),
	vmId: z.number().int().optional()
});

export const VMCPUPinningSchema = z.object({
	id: z.number().int(),
	vmId: z.number().int(),
	hostSocket: z.number().int(),
	hostCpu: z.array(z.number().int())
});

export enum DomainState {
	DomainNostate = 0,
	DomainRunning = 1,
	DomainBlocked = 2,
	DomainPaused = 3,
	DomainShutdown = 4,
	DomainShutoff = 5,
	DomainCrashed = 6,
	DomainPmsuspended = 7
}

export const DomainStateSchema = z.enum(DomainState);

export const VMSchema = z.object({
	id: z.number().int(),
	name: z.string(),
	description: z.string(),
	vmId: z.number().int(),
	cpuSockets: z.number().int(),
	cpuCores: z.number().int(),
	cpuThreads: z.number().int(),
	ram: z.number().int(),
	serial: z.boolean(),
	vncEnabled: z.boolean(),
	vncPort: z.number().int(),
	vncPassword: z.string(),
	vncResolution: z.string(),
	vncWait: z.boolean(),
	startAtBoot: z.boolean(),
	startOrder: z.number().int(),
	wol: z.boolean(),
	timeOffset: z.enum(['utc', 'localtime']),
	state: DomainStateSchema,
	storages: z.array(VMStorageSchema),
	networks: z.array(VMNetworkSchema),
	pciDevices: z.union([z.array(z.number().int()), z.null()]),
	cpuPinning: z.union([z.array(VMCPUPinningSchema), z.null()]),

	createdAt: z.string(),
	updatedAt: z.string(),

	startedAt: z.string().nullable(),
	stoppedAt: z.string().nullable()
});

export const VMStatSchema = z.object({
	vmId: z.number().int(),
	cpuUsage: z.number(),
	memoryUsage: z.number(),
	memoryUsed: z.number(),
	createdAt: z.string()
});

export const VMDomainSchema = z.object({
	id: z.number().int(),
	uuid: z.string(),
	name: z.string(),
	status: z.string()
});

export const SimpleVmSchema = z.object({
	id: z.number().int(),
	name: z.string(),
	vmId: z.number().int(),
	state: DomainStateSchema
});

export type VM = z.infer<typeof VMSchema>;
export type VMStorage = z.infer<typeof VMStorageSchema>;
export type VMNetwork = z.infer<typeof VMNetworkSchema>;
export type VMDomain = z.infer<typeof VMDomainSchema>;
export type VMStat = z.infer<typeof VMStatSchema>;
export type SimpleVm = z.infer<typeof SimpleVmSchema>;

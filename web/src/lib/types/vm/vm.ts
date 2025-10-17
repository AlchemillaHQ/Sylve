import { z } from 'zod/v4';
import { NetworkObjectSchema } from '../network/object';

export interface CreateData {
	name: string;
	node: string;
	id: number;
	description: string;
	storage: {
		type: string;
		guid: string;
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
		pinnedCPUs: number[];
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

export const VMStorageSchema = z.object({
	id: z.number().int(),
	type: z.string(),
	dataset: z.string(),
	size: z.number().int(),
	emulation: z.string(),
	detached: z.boolean().optional(),
	vmId: z.number().int().optional(),
	bootOrder: z.number().int().optional(),
	name: z.string().optional()
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

	state: z.enum(['ACTIVE', 'INACTIVE']),

	storages: z.array(VMStorageSchema),
	networks: z.array(VMNetworkSchema),
	pciDevices: z.union([z.array(z.number().int()), z.null()]),
	cpuPinning: z.union([z.array(z.number().int()), z.null()]),

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
	state: z.string()
});

export type VM = z.infer<typeof VMSchema>;
export type VMStorage = z.infer<typeof VMStorageSchema>;
export type VMNetwork = z.infer<typeof VMNetworkSchema>;
export type VMDomain = z.infer<typeof VMDomainSchema>;
export type VMStat = z.infer<typeof VMStatSchema>;
export type SimpleVm = z.infer<typeof SimpleVmSchema>;

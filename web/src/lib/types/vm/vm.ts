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
        cloudInit: {
            enabled: boolean;
            data: string;
            metadata: string;
            networkConfig: string;
        };
        ignoreUmsrs: boolean;
        qemuGuestAgent: boolean;
    };
}

export type VMStorageType = 'raw' | 'zvol' | 'image';
export type VMStorageEmulationType = 'virtio-blk' | 'ahci-hd' | 'ahci-cd' | 'nvme';

export const VMStorageDatasetSchema = z.object({
    id: z.number().int(),
    pool: z.string(),
    name: z.string(),
    guid: z.string()
});

export const VMStorageSchema = z.object({
    id: z.number().int(),
    vmId: z.number().int().optional(),
    name: z.string().optional(),
    type: z.enum(['raw', 'zvol', 'image']),
    uuid: z.string().optional(),
    pool: z.string().optional().default(''),
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
    rid: z.number().int(),
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
    shutdownWaitTime: z.number().int(),
    cloudInitData: z.string().nullable(),
    cloudInitMetaData: z.string().nullable(),
    cloudInitNetworkConfig: z.string().nullable(),
    ignoreUMSR: z.boolean(),
    qemuGuestAgent: z.boolean(),
    tpmEmulation: z.boolean(),

    createdAt: z.string(),
    updatedAt: z.string(),

    startedAt: z.string().nullable(),
    stoppedAt: z.string().nullable()
});

export const VMStatSchema = z.object({
    vmId: z.number().int().default(0),
    cpuUsage: z.number().default(0),
    memoryUsage: z.number().default(0),
    memoryUsed: z.number().default(0),
    createdAt: z.string().default(new Date(0).toISOString())
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
    rid: z.number().int(),
    vncPort: z.number(),
    state: DomainStateSchema,
    cpuPinning: z.union([z.array(VMCPUPinningSchema), z.null()]),
});

export const QGAOSInfoSchema = z.object({
    name: z.string(),
    "kernel-release": z.string(),
    version: z.string(),
    "pretty-name": z.string(),
    "version-id": z.string(),
    "kernel-version": z.string(),
    machine: z.string(),
    id: z.string()
});

export const QGANetworkIPAddressSchema = z.object({
    "ip-address-type": z.enum(["ipv4", "ipv6"]),
    "ip-address": z.string(),
    prefix: z.number().int()
});

export const QGANetworkStatisticsSchema = z.object({
    "tx-packets": z.number().int(),
    "tx-errs": z.number().int(),
    "rx-bytes": z.number().int(),
    "rx-dropped": z.number().int(),
    "rx-packets": z.number().int(),
    "rx-errs": z.number().int(),
    "tx-bytes": z.number().int(),
    "tx-dropped": z.number().int()
});

export const QGANetworkInterfaceSchema = z.object({
    name: z.string().nullable(),
    "ip-addresses": z.array(QGANetworkIPAddressSchema).nullable(),
    statistics: QGANetworkStatisticsSchema.nullable(),
    "hardware-address": z.string().nullable()
});

export const QGAInfoSchema = z.object({
    osInfo: QGAOSInfoSchema,
    interfaces: z.array(QGANetworkInterfaceSchema).nullable()
});

export type VM = z.infer<typeof VMSchema>;
export type VMCPUPinning = z.infer<typeof VMCPUPinningSchema>;
export type VMStorage = z.infer<typeof VMStorageSchema>;
export type VMNetwork = z.infer<typeof VMNetworkSchema>;
export type VMDomain = z.infer<typeof VMDomainSchema>;
export type VMStat = z.infer<typeof VMStatSchema>;
export type SimpleVm = z.infer<typeof SimpleVmSchema>;
export type QGAInfo = z.infer<typeof QGAInfoSchema>;
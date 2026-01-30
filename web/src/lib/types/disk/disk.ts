import { z } from 'zod/v4';

export const DeviceInfoSchema = z.object({
    name: z.string(),
    info_name: z.string(),
    type: z.string(),
    protocol: z.string()
});

export const PartitionSchema = z.object({
    uuid: z.string(),
    name: z.string(),
    usage: z.string(),
    size: z.number(),
});

export const ATASmartAttributeSchema = z.object({
    id: z.number(),
    name: z.string(),
    value: z.number().optional(),
    worst: z.number().optional(),
    thresh: z.number().optional(),
    raw_value: z.number(),
    raw_string: z.string()
});

export const SmartDataSchema = z.object({
    device: DeviceInfoSchema,
    passed: z.boolean(),
    power_on_hours: z.number(),
    power_cycle_count: z.number(),
    temperature: z.number(),
    attributes: z.array(ATASmartAttributeSchema).optional()
});

export const NvmeCriticalWarningStateSchema = z.object({
    availableSpare: z.number(),
    temperature: z.number(),
    deviceReliability: z.number(),
    readOnly: z.number(),
    volatileMemoryBackup: z.number()
});

export const SmartNVMeSchema = z.object({
    device: DeviceInfoSchema,
    passed: z.boolean(),
    power_on_hours: z.number(),
    power_cycle_count: z.number(),
    temperature: z.number(),

    criticalWarning: z.string(),
    criticalWarningState: NvmeCriticalWarningStateSchema,
    availableSpare: z.number(),
    availableSpareThreshold: z.number(),
    percentageUsed: z.number(),
    dataUnitsRead: z.number(),
    dataUnitsWritten: z.number(),
    hostReadCommands: z.number(),
    hostWriteCommands: z.number(),
    controllerBusyTime: z.number(),
    unsafeShutdowns: z.number(),
    mediaErrors: z.number(),
    errorInfoLogEntries: z.number(),
    warningCompositeTempTime: z.number(),
    errorCompositeTempTime: z.number(),
    temperature1TransitionCnt: z.number(),
    temperature2TransitionCnt: z.number(),
    totalTimeForTemperature1: z.number(),
    totalTimeForTemperature2: z.number()
});

export const DiskSchema = z.object({
    uuid: z.string(),
    device: z.string(),
    type: z.string(),
    usage: z.string(),
    size: z.number(),
    model: z.string(),
    serial: z.string(),
    gpt: z.boolean(),
    smartData: z.union([SmartNVMeSchema, SmartDataSchema, z.null()]).optional(),
    wearOut: z.string(),
    partitions: z.array(PartitionSchema).default([])
});

export const DiskActionSchema = z.object({
    device: z.string()
});


export type SmartAttribute = Record<
    string,
    string | number | boolean | Record<string, string | number | boolean>
>;

export type DeviceInfo = z.infer<typeof DeviceInfoSchema>;
export type ATASmartAttribute = z.infer<typeof ATASmartAttributeSchema>;
export type SmartData = z.infer<typeof SmartDataSchema>;
export type SmartNVMe = z.infer<typeof SmartNVMeSchema>;
export type Disk = z.infer<typeof DiskSchema>;
export type Partition = z.infer<typeof PartitionSchema>;
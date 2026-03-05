import { z } from 'zod/v4';

export const CPUInfoHistoricalSchema = z.array(
    z.object({
        id: z.number().default(0),
        usage: z.number().default(0),
        createdAt: z.string().default('')
    })
);

export const ArchitectureSchema = z.enum([
    'amd64',
    '386',
    'arm',
    'arm64',
    'riscv64',
    'ppc64',
    'ppc64le',
    's390x',
    'wasm',
    'loong64'
]);

export const CPUInfoSchema = z.object({
    name: z.string().default('Unknown'),
    sockets: z.number().default(0),
    architecture: ArchitectureSchema.default('amd64'),
    physicalCores: z.number().default(0),
    threadsPerCore: z.number().default(0),
    logicalCores: z.number().default(0),
    family: z.number().default(0),
    model: z.number().default(0),
    features: z.array(z.string()).default([]),
    cacheLine: z.number().default(0),
    cache: z.object({
        l1d: z.number().default(0),
        l1i: z.number().default(0),
        l2: z.number().default(0),
        l3: z.number().default(0)
    }),
    frequency: z.number().default(0),
    usage: z.number().default(0)
});

export type Architecture = z.infer<typeof ArchitectureSchema>;
export type CPUInfo = z.infer<typeof CPUInfoSchema>;
export type CPUInfoHistorical = z.infer<typeof CPUInfoHistoricalSchema>;

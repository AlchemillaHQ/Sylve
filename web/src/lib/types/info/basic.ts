import { z } from 'zod/v4';
import { ArchitectureSchema } from './cpu';

export const BasicInfoSchema = z.object({
	hostname: z.string().default('Unknown'),
	os: z.string().default('Unknown'),
	uptime: z.number().default(0),
	loadAverage: z.string().default('Unknown'),
	bootMode: z.string().default('Unknown'),
	sylveVersion: z.string().default('Unknown'),
	sylveCommit: z.string().default('unknown'),
	devFSDisabled: z.boolean().default(false),
	architecture: ArchitectureSchema.default('amd64'),
	maxVCPUs: z.number().int().nonnegative().default(0)
});

export type BasicInfo = z.infer<typeof BasicInfoSchema>;

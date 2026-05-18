import { z } from 'zod/v4';

export const BasicInfoSchema = z.object({
	hostname: z.string().default('Unknown'),
	os: z.string().default('Unknown'),
	uptime: z.number().default(0),
	loadAverage: z.string().default('Unknown'),
	bootMode: z.string().default('Unknown'),
	sylveVersion: z.string().default('Unknown'),
	devFSDisabled: z.boolean().default(false)
});

export type BasicInfo = z.infer<typeof BasicInfoSchema>;
